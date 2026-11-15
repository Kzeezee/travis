//go:build windows

package memory

import (
	"os"

	"golang.org/x/sys/windows"
)

func lockExclusive(file *os.File) error {
	var overlapped windows.Overlapped
	return windows.LockFileEx(windows.Handle(file.Fd()), windows.LOCKFILE_EXCLUSIVE_LOCK, 0, ^uint32(0), ^uint32(0), &overlapped)
}

func unlockExclusive(file *os.File) error {
	var overlapped windows.Overlapped
	return windows.UnlockFileEx(windows.Handle(file.Fd()), 0, ^uint32(0), ^uint32(0), &overlapped)
}
