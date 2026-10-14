//go:build windows

package mcp

import (
	"os"

	"golang.org/x/sys/windows"
)

// lockExclusive blocks until it holds an exclusive lock on f (D-32, LockFileEx), the same
// approach internal/security/broker/filelock_windows.go uses for decisions.toml —
// duplicated rather than imported, since tools/mcp must not depend on security/broker, a
// sibling adapter it deliberately doesn't route through.
func lockExclusive(f *os.File) error {
	var overlapped windows.Overlapped
	return windows.LockFileEx(windows.Handle(f.Fd()), windows.LOCKFILE_EXCLUSIVE_LOCK, 0, ^uint32(0), ^uint32(0), &overlapped)
}

func unlockExclusive(f *os.File) error {
	var overlapped windows.Overlapped
	return windows.UnlockFileEx(windows.Handle(f.Fd()), 0, ^uint32(0), ^uint32(0), &overlapped)
}
