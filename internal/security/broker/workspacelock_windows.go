package broker

import (
	"errors"

	"golang.org/x/sys/windows"
)

// stillActiveExitCode is STILL_ACTIVE from the Windows SDK; x/sys/windows has no constant for it.
const stillActiveExitCode = 259

// IsProcessAlive opens the process to read its exit code: STILL_ACTIVE means it is running. A
// pid belonging to a process this user cannot query (ERROR_ACCESS_DENIED — a different user or a
// protected process) is treated as alive, the same safe-side reading the Unix implementation
// gives its analogous EPERM case; only "no such process" says otherwise.
func IsProcessAlive(pid int) bool {
	handle, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid)) //nolint:gosec // an out-of-range pid (os.Getpid(), or a parsed lock file) just makes OpenProcess fail, read as "not alive"
	if err != nil {
		return errors.Is(err, windows.ERROR_ACCESS_DENIED)
	}
	defer func() { _ = windows.CloseHandle(handle) }()
	var exitCode uint32
	if err := windows.GetExitCodeProcess(handle, &exitCode); err != nil {
		return true
	}
	return exitCode == stillActiveExitCode
}
