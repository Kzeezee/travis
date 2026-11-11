//go:build unix

package broker

import (
	"errors"
	"os"
	"syscall"
)

// lockExclusive blocks until it holds an exclusive advisory lock on f (D-32).
func lockExclusive(f *os.File) error {
	return syscall.Flock(int(f.Fd()), syscall.LOCK_EX)
}

func unlockExclusive(f *os.File) error {
	return syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
}

// tryLockExclusive takes an exclusive advisory lock on f without blocking; held reports whether
// it was acquired. Used where a competing holder means "skip, someone else is already doing this"
// rather than "wait" (D-32's retention sweep, run from every workspace against shared StateDir).
func tryLockExclusive(f *os.File) (held bool, err error) {
	err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
	if errors.Is(err, syscall.EWOULDBLOCK) {
		return false, nil
	}
	return err == nil, err
}
