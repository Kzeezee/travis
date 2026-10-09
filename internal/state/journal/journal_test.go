package journal_test

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sync"
	"testing"

	"github.com/Kzeezee/travis/internal/core"
	"github.com/Kzeezee/travis/internal/state/journal"
)

const crashHelperEnv = "TRAVIS_JOURNAL_CRASH_HELPER"

func openJournal(t *testing.T, dir string, session core.ID, maxSnapshotBytes int64) *journal.Journal {
	t.Helper()
	j, err := journal.Open(dir, session, maxSnapshotBytes)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = j.Close() })
	return j
}

func writeFile(t *testing.T, path, data string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func snapshot(t *testing.T, j *journal.Journal, path string) journal.BlobRef {
	t.Helper()
	ref, err := j.Snapshot(context.Background(), path)
	if err != nil {
		t.Fatalf("Snapshot %s: %v", path, err)
	}
	return ref
}

func record(t *testing.T, j *journal.Journal, path string, before journal.BlobRef) {
	t.Helper()
	if err := j.Record("t1", "c1", path, before, ""); err != nil {
		t.Fatalf("Record: %v", err)
	}
}

// listBlobFiles reports every file under blobs, including leftover temp files.
func listBlobFiles(t *testing.T, dir string) []string {
	t.Helper()
	var files []string
	err := filepath.WalkDir(filepath.Join(dir, "blobs"), func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !entry.IsDir() {
			files = append(files, path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return files
}

func buildBlobPath(dir string, ref journal.BlobRef) string {
	return filepath.Join(dir, "blobs", string(ref[:2]), string(ref))
}

func hashRef(data string) journal.BlobRef {
	sum := sha256.Sum256([]byte(data))
	return journal.BlobRef(hex.EncodeToString(sum[:]))
}

func TestSnapshotRestoreRoundTrip(t *testing.T) {
	dir, target := t.TempDir(), filepath.Join(t.TempDir(), "file.txt")
	j := openJournal(t, dir, "s", 1<<20)
	writeFile(t, target, "orig")
	record(t, j, target, snapshot(t, j, target))
	writeFile(t, target, "new")

	restored, err := j.Restore(context.Background(), journal.Selector{Turn: "t1"})
	if err != nil {
		t.Fatalf("Restore: %v", err)
	}
	if !slices.Equal(restored, []string{target}) {
		t.Errorf("restored = %v, want [%s]", restored, target)
	}
	if got := readFile(t, target); got != "orig" {
		t.Errorf("target = %q, want %q", got, "orig")
	}
}

func TestRestoreMissingFileDeletes(t *testing.T) {
	dir, target := t.TempDir(), filepath.Join(t.TempDir(), "new.txt")
	j := openJournal(t, dir, "s", 1<<20)
	ref := snapshot(t, j, target)
	if ref != "" {
		t.Fatalf("ref of absent file = %q, want empty", ref)
	}
	writeFile(t, target, "created")
	record(t, j, target, ref)

	if _, err := j.Restore(context.Background(), journal.Selector{Turn: "t1"}); err != nil {
		t.Fatalf("Restore: %v", err)
	}
	if _, err := os.Stat(target); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("target still exists: %v", err)
	}
}

func TestSnapshotDedup(t *testing.T) {
	dir, files := t.TempDir(), t.TempDir()
	j := openJournal(t, dir, "s", 1<<20)
	first, second := filepath.Join(files, "a"), filepath.Join(files, "b")
	writeFile(t, first, "same")
	writeFile(t, second, "same")

	if a, b := snapshot(t, j, first), snapshot(t, j, second); a != b {
		t.Errorf("refs differ: %q vs %q", a, b)
	}
	if blobs := listBlobFiles(t, dir); len(blobs) != 1 {
		t.Errorf("blob files = %v, want exactly one", blobs)
	}
}

func TestConcurrentIdenticalSnapshots(t *testing.T) {
	dir, target := t.TempDir(), filepath.Join(t.TempDir(), "file.txt")
	journals := []*journal.Journal{openJournal(t, dir, "a", 1<<20), openJournal(t, dir, "b", 1<<20)}
	writeFile(t, target, "shared bytes")

	refs := make([]journal.BlobRef, 16)
	errs := make([]error, 16)
	var wg sync.WaitGroup
	for i := range refs {
		wg.Go(func() {
			refs[i], errs[i] = journals[i%2].Snapshot(context.Background(), target)
		})
	}
	wg.Wait()

	for i := range refs {
		if errs[i] != nil {
			t.Errorf("goroutine %d: %v", i, errs[i])
		}
		if refs[i] != refs[0] {
			t.Errorf("goroutine %d ref = %q, want %q", i, refs[i], refs[0])
		}
	}
	if blobs := listBlobFiles(t, dir); len(blobs) != 1 {
		t.Errorf("blob files = %v, want exactly one", blobs)
	}
}

func TestSnapshotOversizedIsIrreversible(t *testing.T) {
	dir, target := t.TempDir(), filepath.Join(t.TempDir(), "big.txt")
	j := openJournal(t, dir, "s", 4)
	writeFile(t, target, "12345")

	if _, err := j.Snapshot(context.Background(), target); !errors.Is(err, journal.ErrIrreversible) {
		t.Fatalf("err = %v, want ErrIrreversible", err)
	}
	if blobs := listBlobFiles(t, dir); len(blobs) != 0 {
		t.Errorf("blob files = %v, want none", blobs)
	}
}

func TestSnapshotDirectoryIsIrreversible(t *testing.T) {
	j := openJournal(t, t.TempDir(), "s", 1<<20)
	if _, err := j.Snapshot(context.Background(), t.TempDir()); !errors.Is(err, journal.ErrIrreversible) {
		t.Fatalf("err = %v, want ErrIrreversible", err)
	}
}

func TestRestoreSeveralChangesInOneTurn(t *testing.T) {
	dir, target := t.TempDir(), filepath.Join(t.TempDir(), "file.txt")
	j := openJournal(t, dir, "s", 1<<20)
	writeFile(t, target, "v1")
	record(t, j, target, snapshot(t, j, target))
	writeFile(t, target, "v2")
	record(t, j, target, snapshot(t, j, target))
	writeFile(t, target, "v3")

	restored, err := j.Restore(context.Background(), journal.Selector{Turn: "t1"})
	if err != nil {
		t.Fatalf("Restore: %v", err)
	}
	if !slices.Equal(restored, []string{target}) {
		t.Errorf("restored = %v, want [%s] once", restored, target)
	}
	if got := readFile(t, target); got != "v1" {
		t.Errorf("target = %q, want %q", got, "v1")
	}
}

func TestReopenReplaysLog(t *testing.T) {
	dir, files := t.TempDir(), t.TempDir()
	j, err := journal.Open(dir, "s", 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	record(t, j, filepath.Join(files, "a"), "")
	record(t, j, filepath.Join(files, "b"), hashRef("b"))
	want := j.Changes("t1")
	if err := j.Close(); err != nil {
		t.Fatal(err)
	}

	if got := openJournal(t, dir, "s", 1<<20).Changes("t1"); !slices.Equal(got, want) || len(got) != 2 {
		t.Errorf("replayed changes = %v, want %v", got, want)
	}
}

func TestCorruptTailLoads(t *testing.T) {
	dir, target := t.TempDir(), filepath.Join(t.TempDir(), "file.txt")
	j, err := journal.Open(dir, "s", 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	record(t, j, target, "")
	want := j.Changes("t1")
	if err := j.Close(); err != nil {
		t.Fatal(err)
	}
	log, err := os.OpenFile(filepath.Join(dir, "s.jsonl"), os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := log.WriteString(`{"turn":"t1","pa`); err != nil {
		t.Fatal(err)
	}
	if err := log.Close(); err != nil {
		t.Fatal(err)
	}

	if got := openJournal(t, dir, "s", 1<<20).Changes("t1"); !slices.Equal(got, want) {
		t.Errorf("changes after torn tail = %v, want %v", got, want)
	}
}

func TestRestoreRejectsCorruptBlob(t *testing.T) {
	dir, target := t.TempDir(), filepath.Join(t.TempDir(), "file.txt")
	j := openJournal(t, dir, "s", 1<<20)
	writeFile(t, target, "orig")
	ref := snapshot(t, j, target)
	record(t, j, target, ref)
	writeFile(t, target, "new")
	blob := buildBlobPath(dir, ref)
	data := []byte(readFile(t, blob))
	data[0] ^= 0xff
	if err := os.WriteFile(blob, data, 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := j.Restore(context.Background(), journal.Selector{Turn: "t1"}); err == nil {
		t.Fatal("Restore succeeded with a corrupt blob")
	}
	if got := readFile(t, target); got != "new" {
		t.Errorf("target = %q, want untouched %q", got, "new")
	}
}

func TestRelativePathRejected(t *testing.T) {
	j := openJournal(t, t.TempDir(), "s", 1<<20)
	if _, err := j.Snapshot(context.Background(), "rel.txt"); err == nil {
		t.Error("Snapshot accepted a relative path")
	}
	if err := j.Record("t1", "c1", "rel.txt", "", ""); err == nil {
		t.Error("Record accepted a relative path")
	}
}

// TestCrashHelper is the child process TestCrashBetweenSnapshotAndWrite kills mid-operation.
func TestCrashHelper(t *testing.T) {
	if os.Getenv(crashHelperEnv) == "" {
		t.Skip("runs only as a crash-test child process")
	}
	target := os.Getenv("TARGET")
	j, err := journal.Open(os.Getenv("JOURNAL_DIR"), "crash", 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	ref, err := j.Snapshot(context.Background(), target)
	if err != nil {
		t.Fatal(err)
	}
	if os.Getenv("KILL_AFTER") == "record" {
		if err := j.Record("t1", "c1", target, ref, ""); err != nil {
			t.Fatal(err)
		}
	}
	fmt.Println("ready")
	select {}
}

func TestCrashBetweenSnapshotAndWrite(t *testing.T) {
	for _, killAfter := range []string{"snapshot", "record"} {
		t.Run("after-"+killAfter, func(t *testing.T) {
			dir, target := t.TempDir(), filepath.Join(t.TempDir(), "file.txt")
			const original = "original bytes"
			writeFile(t, target, original)
			runCrashHelperUntilKilled(t, dir, target, killAfter)

			if _, err := os.Stat(buildBlobPath(dir, hashRef(original))); err != nil {
				t.Errorf("blob missing after crash: %v", err)
			}
			if got := readFile(t, target); got != original {
				t.Errorf("target = %q, want untouched %q", got, original)
			}
			j := openJournal(t, dir, "crash", 1<<20)
			wantChanges := 0
			if killAfter == "record" {
				wantChanges = 1
			}
			if got := j.Changes("t1"); len(got) != wantChanges {
				t.Fatalf("changes = %v, want %d", got, wantChanges)
			}
			if killAfter != "record" {
				return
			}
			writeFile(t, target, "written after crash")
			if _, err := j.Restore(context.Background(), journal.Selector{Turn: "t1"}); err != nil {
				t.Fatalf("Restore: %v", err)
			}
			if got := readFile(t, target); got != original {
				t.Errorf("restored target = %q, want %q", got, original)
			}
		})
	}
}

func runCrashHelperUntilKilled(t *testing.T, dir, target, killAfter string) {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^TestCrashHelper$")
	cmd.Env = append(os.Environ(), crashHelperEnv+"=1", "JOURNAL_DIR="+dir, "TARGET="+target, "KILL_AFTER="+killAfter)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	// Wait releases the child's file handles, which Windows needs before TempDir cleanup.
	defer func() { _ = cmd.Wait() }()
	defer func() { _ = cmd.Process.Kill() }()

	scanner := bufio.NewScanner(stdout)
	for scanner.Scan() {
		if scanner.Text() == "ready" {
			return
		}
	}
	t.Fatalf("crash helper exited before ready: %v", scanner.Err())
}
