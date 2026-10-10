//go:build unix

package broker

import (
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
