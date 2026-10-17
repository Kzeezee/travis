//go:build linux || darwin

package sandbox

import (
	"errors"
	"fmt"
	"os/exec"
	"syscall"
)

// killGroupOnCancel makes the launcher lead a process group that the target and its descendants inherit,
// and cancellation kills the whole group. A descendant that calls setsid leaves the group and outlives it.
func killGroupOnCancel(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL); err != nil && !errors.Is(err, syscall.ESRCH) {
			return fmt.Errorf("kill sandboxed process group: %w", err)
		}
		return nil
	}
}
