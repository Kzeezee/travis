package broker_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/Kzeezee/travis/internal/core"
	"github.com/Kzeezee/travis/internal/security/broker"
	"github.com/Kzeezee/travis/internal/state/blobs"
	"github.com/Kzeezee/travis/internal/state/session/filestore"
)

func writeFileChangedEntry(t *testing.T, log *filestore.Store, before string) {
	t.Helper()
	path, err := filepath.Abs("a.go")
	if err != nil {
		t.Fatal(err)
	}
	entry := core.SessionEntry{
		ID: "e1", At: time.Now(), Author: core.AuthorBroker, Turn: "t1", Kind: core.EntryFileChanged,
		FileChange: &core.FileChange{Path: path, Before: before, After: string(blobs.HashContent([]byte("new")))},
	}
	if err := log.Append(entry); err != nil {
		t.Fatalf("Append: %v", err)
	}
}

// TestSweepExpiredSessionsEndToEnd exercises the whole ticket: an expired, dead session's log
// and blob are gone; a within-retention session's log and the blob it still refers to survive
// and the blob still restores through blobs.Restore (TRA-78's "done when").
func TestSweepExpiredSessionsEndToEnd(t *testing.T) {
	stateDir := t.TempDir()
	sessionsDir := filepath.Join(stateDir, "sessions")
	blobsDir := filepath.Join(stateDir, "blobs")

	backups, err := blobs.Open(blobsDir, 1<<20)
	if err != nil {
		t.Fatalf("blobs.Open: %v", err)
	}
	keptRef, err := backups.Snapshot(context.Background(), mustAbsPath(t, "kept.txt", "kept bytes"))
	if err != nil {
		t.Fatalf("snapshot kept blob: %v", err)
	}
	orphanRef, err := backups.Snapshot(context.Background(), mustAbsPath(t, "orphan.txt", "orphan bytes"))
	if err != nil {
		t.Fatalf("snapshot orphan blob: %v", err)
	}

	expiredLog, err := filestore.Open(sessionsDir, "expired")
	if err != nil {
		t.Fatalf("open expired log: %v", err)
	}
	writeFileChangedEntry(t, expiredLog, string(orphanRef))
	expiredLog.Close()

	keptLog, err := filestore.Open(sessionsDir, "kept")
	if err != nil {
		t.Fatalf("open kept log: %v", err)
	}
	writeFileChangedEntry(t, keptLog, string(keptRef))
	keptLog.Close()

	ageFile(t, filepath.Join(sessionsDir, "expired.jsonl"), 2*time.Hour)
	ageFile(t, filepath.Join(blobsDir, string(orphanRef[:2]), string(orphanRef)), 2*time.Hour)
	// kept.jsonl and its blob are left at their just-written mtime, inside the grace window.

	report, err := broker.SweepExpiredSessions(stateDir, time.Hour, core.SystemClock{})
	if err != nil {
		t.Fatalf("SweepExpiredSessions: %v", err)
	}
	if report.ExpiredSessions != 1 || report.DeletedBlobs != 1 {
		t.Fatalf("report = %+v, want 1 expired session and 1 deleted blob", report)
	}

	if _, err := os.Stat(filepath.Join(sessionsDir, "expired.jsonl")); !os.IsNotExist(err) {
		t.Fatalf("expired.jsonl still exists, err = %v", err)
	}
	if _, err := os.Stat(filepath.Join(sessionsDir, "kept.jsonl")); err != nil {
		t.Fatalf("kept.jsonl was removed: %v", err)
	}
	if err := backups.Restore(context.Background(), mustAbsPath(t, "restored.txt", ""), keptRef); err != nil {
		t.Fatalf("restore surviving blob: %v", err)
	}
}

// TestSweepExpiredSessionsKeepsASessionWithNoShutdownDeletion is the regression the plan review
// called for: a still-alive session's marker is never proactively removed on shutdown, so the
// only thing standing between it and deletion is the sweep's own liveness check. This spawns a
// real live process to make sure that check alone is what keeps it.
func TestSweepExpiredSessionsKeepsASessionWithNoShutdownDeletion(t *testing.T) {
	stateDir := t.TempDir()
	sessionsDir := filepath.Join(stateDir, "sessions")
	writeAgedSessionLog(t, sessionsDir, "live", 2*time.Hour)

	cmd, stop := startLongRunningProcess(t)
	defer stop()
	if err := broker.WriteSessionLivenessMarker(sessionsDir, "live", cmd.Process.Pid); err != nil {
		t.Fatalf("WriteSessionLivenessMarker: %v", err)
	}
	ageFile(t, filepath.Join(sessionsDir, "live.lock"), 2*time.Hour)

	report, err := broker.SweepExpiredSessions(stateDir, time.Hour, core.SystemClock{})
	if err != nil {
		t.Fatalf("SweepExpiredSessions: %v", err)
	}
	if report.ExpiredSessions != 0 {
		t.Fatalf("report = %+v, want the live session kept", report)
	}
	if _, err := os.Stat(filepath.Join(sessionsDir, "live.jsonl")); err != nil {
		t.Fatalf("live.jsonl was removed while its process is still running: %v", err)
	}
}

func mustAbsPath(t *testing.T, name, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if content != "" {
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return path
}

func ageFile(t *testing.T, path string, age time.Duration) {
	t.Helper()
	old := time.Now().Add(-age)
	if err := os.Chtimes(path, old, old); err != nil {
		t.Fatal(err)
	}
}

func writeAgedSessionLog(t *testing.T, sessionsDir, id string, age time.Duration) {
	t.Helper()
	log, err := filestore.Open(sessionsDir, core.ID(id))
	if err != nil {
		t.Fatalf("open %s log: %v", id, err)
	}
	entry := core.SessionEntry{ID: "e1", At: time.Now(), Author: core.AuthorTravis, Turn: "t1", Kind: core.EntryMessage,
		Message: &core.Message{Role: core.RoleUser, Parts: []core.Part{core.TextPart{Text: "hi"}}}}
	if err := log.Append(entry); err != nil {
		t.Fatalf("append to %s log: %v", id, err)
	}
	log.Close()
	ageFile(t, filepath.Join(sessionsDir, id+".jsonl"), age)
}

func startLongRunningProcess(t *testing.T) (*exec.Cmd, func()) {
	t.Helper()
	var cmd *exec.Cmd
	if os.PathSeparator == '\\' {
		// Neither pause nor timeout works here: both require an attached console, which go
		// test does not provide, so they exit instantly instead of waiting. ping against the
		// loopback address needs no console and reliably takes ~30s for 30 pings.
		cmd = exec.CommandContext(context.Background(), "ping", "-n", "31", "127.0.0.1")
	} else {
		cmd = exec.CommandContext(context.Background(), "sleep", "30")
	}
	if err := cmd.Start(); err != nil {
		t.Fatalf("start long-running process: %v", err)
	}
	return cmd, func() { _ = cmd.Process.Kill(); _ = cmd.Wait() }
}
