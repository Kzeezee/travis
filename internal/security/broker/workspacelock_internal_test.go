package broker

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

func alwaysAlive(int) bool { return true }
func neverAlive(int) bool  { return false }

func TestClaimWorkspaceWarnsOnlyAboutAnotherLiveSession(t *testing.T) {
	workspaceDir := t.TempDir()

	if warning := claimWorkspace(workspaceDir, 111, alwaysAlive); warning != "" {
		t.Fatalf("first claim: warning = %q, want none", warning)
	}
	assertLockFileHoldsPID(t, workspaceDir, 111)

	warning := claimWorkspace(workspaceDir, 222, alwaysAlive)
	if warning == "" {
		t.Fatal("second claim with a live other pid: want a warning, got none")
	}
	assertLockFileHoldsPID(t, workspaceDir, 222)
}

func TestClaimWorkspaceIgnoresAStaleMarker(t *testing.T) {
	workspaceDir := t.TempDir()

	if warning := claimWorkspace(workspaceDir, 111, neverAlive); warning != "" {
		t.Fatalf("first claim: warning = %q, want none", warning)
	}
	if warning := claimWorkspace(workspaceDir, 222, neverAlive); warning != "" {
		t.Fatalf("claim over a stale marker: warning = %q, want none", warning)
	}
	assertLockFileHoldsPID(t, workspaceDir, 222)
}

func TestClaimWorkspaceNeverWarnsAboutItsOwnEarlierMarker(t *testing.T) {
	workspaceDir := t.TempDir()

	claimWorkspace(workspaceDir, 111, alwaysAlive)
	if warning := claimWorkspace(workspaceDir, 111, alwaysAlive); warning != "" {
		t.Fatalf("re-claiming with the same pid: warning = %q, want none", warning)
	}
}

func TestClaimWorkspaceDirIsPrivateToTheUser(t *testing.T) {
	if os.PathSeparator != '/' {
		t.Skip("POSIX permission bits")
	}
	workspaceDir := t.TempDir()
	claimWorkspace(workspaceDir, 111, alwaysAlive)

	dir := filepath.Dir(ReportWorkspaceLockFile(workspaceDir))
	info, err := os.Stat(dir)
	if err != nil || info.Mode().Perm() != privateStateDirMode {
		t.Fatalf("%s mode = %v, err = %v, want %v", dir, info.Mode().Perm(), err, privateStateDirMode)
	}
}

func assertLockFileHoldsPID(t *testing.T, workspaceDir string, wantPID int) {
	t.Helper()
	data, err := os.ReadFile(ReportWorkspaceLockFile(workspaceDir))
	if err != nil {
		t.Fatalf("read lock file: %v", err)
	}
	if got, _ := strconv.Atoi(string(data)); got != wantPID {
		t.Fatalf("lock file holds pid %d, want %d", got, wantPID)
	}
}
