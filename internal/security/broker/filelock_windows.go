//go:build windows

package broker

import (
	"os"

	"golang.org/x/sys/windows"
)

// lockExclusive blocks until it holds an exclusive lock on f (D-32, LockFileEx).
func lockExclusive(f *os.File) error {
	var overlapped windows.Overlapped
	return windows.LockFileEx(windows.Handle(f.Fd()), windows.LOCKFILE_EXCLUSIVE_LOCK, 0, ^uint32(0), ^uint32(0), &overlapped)
}

func unlockExclusive(f *os.File) error {
	var overlapped windows.Overlapped
	return windows.UnlockFileEx(windows.Handle(f.Fd()), 0, ^uint32(0), ^uint32(0), &overlapped)
}
