package broker

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Kzeezee/travis/internal/core"
	"github.com/Kzeezee/travis/internal/state/blobs"
)

const testRetention = 24 * time.Hour

func writeAgedFile(t *testing.T, path string, age time.Duration, now time.Time) {
	t.Helper()
	if err := os.WriteFile(path, []byte("x"), privateStateFileMode); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, now.Add(-age), now.Add(-age)); err != nil {
		t.Fatal(err)
	}
}

func TestReportExpiredSessionIDsPicksOnlyExpiredAndDeadSessions(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()

	// Expired, dead, no marker at all: picked.
	writeAgedFile(t, filepath.Join(dir, "expired-dead.jsonl"), testRetention+time.Hour, now)

	// Expired, but its marker names a pid isAlive reports as running: kept.
	writeAgedFile(t, filepath.Join(dir, "expired-live.jsonl"), testRetention+time.Hour, now)
	writeAgedFile(t, filepath.Join(dir, "expired-live.lock"), testRetention+time.Hour, now)
	if err := os.WriteFile(filepath.Join(dir, "expired-live.lock"), []byte("999"), privateStateFileMode); err != nil {
		t.Fatal(err)
	}

	// Fresh (inside the grace window) and its marker's pid is dead: kept anyway, too new to trust.
	writeAgedFile(t, filepath.Join(dir, "fresh-dead.jsonl"), time.Minute, now)

	// Not past retention at all: kept.
	writeAgedFile(t, filepath.Join(dir, "young.jsonl"), time.Hour, now)

	isAlive := func(pid int) bool { return pid == 999 }
	expired, err := ReportExpiredSessionIDs(dir, testRetention, now, isAlive)
	if err != nil {
		t.Fatalf("ReportExpiredSessionIDs: %v", err)
	}
	if len(expired) != 1 || string(expired[0]) != "expired-dead" {
		t.Fatalf("expired = %v, want [expired-dead]", expired)
	}
}

func TestReportExpiredSessionIDsKeepsASessionWhoseMarkerIsNewerThanItsLog(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()

	writeAgedFile(t, filepath.Join(dir, "s.jsonl"), testRetention+time.Hour, now)
	// The marker itself is fresh even though the log looks expired: the session may have just
	// restarted (e.g. after a fork reused an old log), so it must not be treated as abandoned.
	writeAgedFile(t, filepath.Join(dir, "s.lock"), time.Minute, now)
	if err := os.WriteFile(filepath.Join(dir, "s.lock"), []byte("123"), privateStateFileMode); err != nil {
		t.Fatal(err)
	}

	expired, err := ReportExpiredSessionIDs(dir, testRetention, now, func(int) bool { return false })
	if err != nil {
		t.Fatalf("ReportExpiredSessionIDs: %v", err)
	}
	if len(expired) != 0 {
		t.Fatalf("expired = %v, want none (marker is within the grace window)", expired)
	}
}

func TestSweepExpiredSessionsSkipsWhenAnotherSweepHoldsTheLock(t *testing.T) {
	stateDir := t.TempDir()
	lockFile, err := os.OpenFile(filepath.Join(stateDir, sweepLockName), os.O_CREATE|os.O_RDWR, privateStateFileMode)
	if err != nil {
		t.Fatal(err)
	}
	defer lockFile.Close()
	if held, err := tryLockExclusive(lockFile); err != nil || !held {
		t.Fatalf("hold sweep lock: held=%v err=%v", held, err)
	}

	sessionsDir := filepath.Join(stateDir, "sessions")
	if err := os.MkdirAll(sessionsDir, privateStateDirMode); err != nil {
		t.Fatal(err)
	}
	writeAgedFile(t, filepath.Join(sessionsDir, "old.jsonl"), 2*time.Hour, time.Now())

	report, err := SweepExpiredSessions(stateDir, time.Hour, core.SystemClock{})
	if err != nil {
		t.Fatalf("SweepExpiredSessions: %v", err)
	}
	if !report.SkippedConcurrentSweep {
		t.Fatalf("report = %+v, want SkippedConcurrentSweep", report)
	}
	if _, err := os.Stat(filepath.Join(sessionsDir, "old.jsonl")); err != nil {
		t.Fatalf("old.jsonl was removed despite the sweep being skipped: %v", err)
	}
}

func TestIsValidBlobRefRejectsAnythingNotA64CharHexName(t *testing.T) {
	valid := strings.Repeat("ab", 32)
	for name, ok := range map[string]bool{
		valid:               true,
		valid[:63]:          false,
		valid + "e":         false,
		"../../etc/passwd":  false,
		"not-hex-at-all-00": false,
	} {
		if got := isValidBlobRef(blobs.Ref(name)); got != ok {
			t.Errorf("isValidBlobRef(%q) = %v, want %v", name, got, ok)
		}
	}
}
