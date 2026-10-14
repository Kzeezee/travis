//go:build unix

package mcp

import (
	"os"
	"syscall"
)

// lockExclusive blocks until it holds an exclusive advisory lock on f (D-32), the same
// approach internal/security/broker/filelock_unix.go uses for decisions.toml — duplicated
// rather than imported, since tools/mcp must not depend on security/broker, a sibling
// adapter it deliberately doesn't route through.
func lockExclusive(f *os.File) error {
	return syscall.Flock(int(f.Fd()), syscall.LOCK_EX)
}

func unlockExclusive(f *os.File) error {
	return syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
}
