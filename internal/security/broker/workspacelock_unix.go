//go:build linux || darwin

package broker

import "syscall"

// isProcessAlive sends the null signal (kill -0): it delivers no signal but still fails with
// ESRCH if pid does not exist. EPERM means it exists but is owned by another user, which this
// workspace's other session never would be, so that case is treated as alive to be safe.
func isProcessAlive(pid int) bool {
	err := syscall.Kill(pid, syscall.Signal(0))
	return err == nil || err == syscall.EPERM
}
