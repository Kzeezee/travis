package broker

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// IsProcessAlive is implemented per OS (workspacelock_unix.go, workspacelock_windows.go,
// workspacelock_other.go), since only the OS can say whether a PID is still running. It is
// exported so retention.go's sweep can reuse the same liveness check against a session's
// own .lock marker.

// ReportWorkspaceLockFile is where the current session's PID is recorded, so the next session to
// start in this workspace can read it. It sits beside decisions.toml (D-30, D-32 rule 5): both
// are shared, workspace-level state the broker writes directly, never through fs_write.
func ReportWorkspaceLockFile(workspaceDir string) string {
	return filepath.Join(workspaceDir, ".travis", "session.lock")
}

// ClaimWorkspace records this session's PID as the one now using workspaceDir, and reports a
// one-line warning if another session's PID was already there and is still running (D-32 rule
// 5). A marker left by a crashed session is stale and is overwritten without warning. Claiming
// never blocks or fails the caller's startup: an I/O problem here is reported as a warning too,
// not an error, since nothing about using the workspace depends on this marker.
func ClaimWorkspace(workspaceDir string, pid int) string {
	return claimWorkspace(workspaceDir, pid, IsProcessAlive)
}

// claimWorkspace takes isAlive as a parameter so tests can fake process liveness without
// starting or killing a real process.
func claimWorkspace(workspaceDir string, pid int, isAlive func(int) bool) string {
	lockFile := ReportWorkspaceLockFile(workspaceDir)
	// One open, not a separate read then write: openPrivateFileNoFollow refuses a symlink
	// planted at .travis or at session.lock (R-26), which a separate os.ReadFile/os.WriteFile
	// pair would not.
	file, err := openPrivateFileNoFollow(lockFile)
	if err != nil {
		return fmt.Sprintf("workspace lock: could not open %q: %v", lockFile, err)
	}
	defer file.Close()

	warning := reportOtherLiveSessionWarning(file, pid, isAlive)
	if err := writeWorkspaceLockFile(file, pid); err != nil {
		note := fmt.Sprintf("workspace lock: could not record this session in %q: %v", lockFile, err)
		if warning != "" {
			return warning + "; " + note
		}
		return note
	}
	return warning
}

func reportOtherLiveSessionWarning(file io.Reader, pid int, isAlive func(int) bool) string {
	data, err := io.ReadAll(file)
	if err != nil {
		return "" // unreadable: nothing to warn about
	}
	otherPID, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil || otherPID == pid || !isAlive(otherPID) {
		return ""
	}
	return fmt.Sprintf("another travis session (pid %d) is already using this workspace; edits in both may overwrite each other — consider a separate `git worktree` for this session", otherPID)
}

func writeWorkspaceLockFile(file *os.File, pid int) error {
	if err := file.Truncate(0); err != nil {
		return err
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return err
	}
	if _, err := file.Write([]byte(strconv.Itoa(pid))); err != nil {
		return err
	}
	return file.Sync()
}
