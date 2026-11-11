//go:build windows

package broker

import (
	"errors"
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

// tryLockExclusive takes an exclusive lock on f without blocking; held reports whether it was
// acquired. Used where a competing holder means "skip, someone else is already doing this" rather
// than "wait" (D-32's retention sweep, run from every workspace against shared StateDir).
func tryLockExclusive(f *os.File) (held bool, err error) {
	var overlapped windows.Overlapped
	err = windows.LockFileEx(windows.Handle(f.Fd()), windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, ^uint32(0), ^uint32(0), &overlapped)
	if errors.Is(err, windows.ERROR_LOCK_VIOLATION) {
		return false, nil
	}
	return err == nil, err
}
