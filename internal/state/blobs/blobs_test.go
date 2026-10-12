package blobs_test

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"

	"github.com/Kzeezee/travis/internal/state/blobs"
)

const crashHelperEnv = "TRAVIS_BLOBS_CRASH_HELPER"

func openStore(t *testing.T, dir string, maxSnapshotBytes int64) *blobs.Store {
	t.Helper()
	store, err := blobs.Open(dir, maxSnapshotBytes)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	return store
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

func snapshot(t *testing.T, store *blobs.Store, path string) blobs.Ref {
	t.Helper()
	ref, err := store.Snapshot(context.Background(), path)
	if err != nil {
		t.Fatalf("Snapshot %s: %v", path, err)
	}
	return ref
}

// listBlobFiles reports every file in the store, including leftover temp files.
func listBlobFiles(t *testing.T, dir string) []string {
	t.Helper()
	var files []string
	err := filepath.WalkDir(dir, func(path string, entry fs.DirEntry, err error) error {
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

func buildBlobPath(dir string, ref blobs.Ref) string {
	return filepath.Join(dir, string(ref[:2]), string(ref))
}

func TestSnapshotRestoreRoundTrip(t *testing.T) {
	dir, target := t.TempDir(), filepath.Join(t.TempDir(), "file.txt")
	store := openStore(t, dir, 1<<20)
	writeFile(t, target, "orig")
	ref := snapshot(t, store, target)
	if ref != blobs.HashContent([]byte("orig")) {
		t.Fatalf("ref = %q, want the hash of the bytes", ref)
	}
	writeFile(t, target, "new")

	if err := store.Restore(context.Background(), target, ref); err != nil {
		t.Fatalf("Restore: %v", err)
	}
	if got := readFile(t, target); got != "orig" {
		t.Errorf("target = %q, want %q", got, "orig")
	}
}

func TestRestoreOfAnAbsentFileDeletesIt(t *testing.T) {
	store := openStore(t, t.TempDir(), 1<<20)
	target := filepath.Join(t.TempDir(), "new.txt")
	ref := snapshot(t, store, target)
	if ref != "" {
		t.Fatalf("ref of absent file = %q, want empty", ref)
	}
	writeFile(t, target, "created")

	if err := store.Restore(context.Background(), target, ref); err != nil {
		t.Fatalf("Restore: %v", err)
	}
	if _, err := os.Stat(target); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("target still exists: %v", err)
	}
	if err := store.Restore(context.Background(), target, ""); err != nil {
		t.Errorf("Restore of an already-absent file = %v, want nil", err)
	}
}

func TestRestoreKeepsTheFilesMode(t *testing.T) {
	store := openStore(t, t.TempDir(), 1<<20)
	target := filepath.Join(t.TempDir(), "script.sh")
	writeFile(t, target, "v1")
	if err := os.Chmod(target, 0o750); err != nil {
		t.Fatal(err)
	}
	ref := snapshot(t, store, target)
	writeFile(t, target, "v2")

	if err := store.Restore(context.Background(), target, ref); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(target)
	if err != nil {
		t.Fatal(err)
	}
	if want := os.FileMode(0o750); info.Mode().Perm() != want && os.PathSeparator == '/' {
		t.Errorf("mode = %v, want %v", info.Mode().Perm(), want)
	}
	if leftovers, _ := filepath.Glob(filepath.Join(filepath.Dir(target), ".travis-restore-*")); len(leftovers) != 0 {
		t.Errorf("temp files left behind: %v", leftovers)
	}
}

func TestSnapshotDedup(t *testing.T) {
	dir, files := t.TempDir(), t.TempDir()
	store := openStore(t, dir, 1<<20)
	first, second := filepath.Join(files, "a"), filepath.Join(files, "b")
	writeFile(t, first, "same")
	writeFile(t, second, "same")

	if a, b := snapshot(t, store, first), snapshot(t, store, second); a != b {
		t.Errorf("refs differ: %q vs %q", a, b)
	}
	if found := listBlobFiles(t, dir); len(found) != 1 {
		t.Errorf("blob files = %v, want exactly one", found)
	}
}

func TestConcurrentIdenticalSnapshots(t *testing.T) {
	dir, target := t.TempDir(), filepath.Join(t.TempDir(), "file.txt")
	stores := []*blobs.Store{openStore(t, dir, 1<<20), openStore(t, dir, 1<<20)}
	writeFile(t, target, "shared bytes")

	refs := make([]blobs.Ref, 16)
	errs := make([]error, 16)
	var wg sync.WaitGroup
	for i := range refs {
		wg.Go(func() {
			refs[i], errs[i] = stores[i%2].Snapshot(context.Background(), target)
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
	if found := listBlobFiles(t, dir); len(found) != 1 {
		t.Errorf("blob files = %v, want exactly one", found)
	}
}

func TestSnapshotOversizedIsIrreversible(t *testing.T) {
	dir, target := t.TempDir(), filepath.Join(t.TempDir(), "big.txt")
	store := openStore(t, dir, 4)
	writeFile(t, target, "12345")

	if _, err := store.Snapshot(context.Background(), target); !errors.Is(err, blobs.ErrIrreversible) {
		t.Fatalf("err = %v, want ErrIrreversible", err)
	}
	if found := listBlobFiles(t, dir); len(found) != 0 {
		t.Errorf("blob files = %v, want none", found)
	}
}

func TestSnapshotDirectoryIsIrreversible(t *testing.T) {
	store := openStore(t, t.TempDir(), 1<<20)
	if _, err := store.Snapshot(context.Background(), t.TempDir()); !errors.Is(err, blobs.ErrIrreversible) {
		t.Fatalf("err = %v, want ErrIrreversible", err)
	}
}

func TestRestoreRejectsCorruptBlob(t *testing.T) {
	dir, target := t.TempDir(), filepath.Join(t.TempDir(), "file.txt")
	store := openStore(t, dir, 1<<20)
	writeFile(t, target, "orig")
	ref := snapshot(t, store, target)
	writeFile(t, target, "new")
	blob := buildBlobPath(dir, ref)
	data := []byte(readFile(t, blob))
	data[0] ^= 0xff
	if err := os.WriteFile(blob, data, 0o600); err != nil {
		t.Fatal(err)
	}

	if err := store.Restore(context.Background(), target, ref); err == nil {
		t.Fatal("Restore succeeded with a corrupt blob")
	}
	if got := readFile(t, target); got != "new" {
		t.Errorf("target = %q, want untouched %q", got, "new")
	}
}

func TestRestoreRejectsRefsThatAreNotDigests(t *testing.T) {
	store := openStore(t, t.TempDir(), 1<<20)
	target := filepath.Join(t.TempDir(), "file.txt")
	for _, ref := range []blobs.Ref{"../../etc/passwd", "abc", blobs.Ref(fmt.Sprintf("%064d", 0)[:63] + "z")} {
		if err := store.Restore(context.Background(), target, ref); err == nil {
			t.Errorf("Restore accepted ref %q", ref)
		}
	}
}

func TestRelativePathRejected(t *testing.T) {
	store := openStore(t, t.TempDir(), 1<<20)
	if _, err := store.Snapshot(context.Background(), "rel.txt"); err == nil {
		t.Error("Snapshot accepted a relative path")
	}
	if err := store.Restore(context.Background(), "rel.txt", ""); err == nil {
		t.Error("Restore accepted a relative path")
	}
}

// TestCrashHelper is the child process TestCrashAfterSnapshotKeepsTheBackup kills mid-operation.
func TestCrashHelper(t *testing.T) {
	if os.Getenv(crashHelperEnv) == "" {
		t.Skip("runs only as a crash-test child process")
	}
	store, err := blobs.Open(os.Getenv("BLOBS_DIR"), 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Snapshot(context.Background(), os.Getenv("TARGET")); err != nil {
		t.Fatal(err)
	}
	fmt.Println("ready")
	select {}
}

// A kill -9 straight after Snapshot returns still leaves a backup that restores (§5.15.1).
func TestCrashAfterSnapshotKeepsTheBackup(t *testing.T) {
	dir, target := t.TempDir(), filepath.Join(t.TempDir(), "file.txt")
	const original = "original bytes"
	writeFile(t, target, original)
	runCrashHelperUntilKilled(t, dir, target)

	ref := blobs.HashContent([]byte(original))
	if _, err := os.Stat(buildBlobPath(dir, ref)); err != nil {
		t.Fatalf("blob missing after crash: %v", err)
	}
	writeFile(t, target, "written after crash")
	if err := openStore(t, dir, 1<<20).Restore(context.Background(), target, ref); err != nil {
		t.Fatalf("Restore: %v", err)
	}
	if got := readFile(t, target); got != original {
		t.Errorf("restored target = %q, want %q", got, original)
	}
}

func runCrashHelperUntilKilled(t *testing.T, dir, target string) {
	t.Helper()
	cmd := exec.CommandContext(t.Context(), os.Args[0], "-test.run=^TestCrashHelper$")
	cmd.Env = append(os.Environ(), crashHelperEnv+"=1", "BLOBS_DIR="+dir, "TARGET="+target)
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
