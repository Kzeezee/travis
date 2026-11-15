//go:build unix

package config

import (
	"os"
	"syscall"
)

func lockExclusive(file *os.File) error {
	return syscall.Flock(int(file.Fd()), syscall.LOCK_EX)
}

func unlockExclusive(file *os.File) error {
	return syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
}
