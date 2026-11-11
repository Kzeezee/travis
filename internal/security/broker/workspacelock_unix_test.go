//go:build linux || darwin

package broker

import (
	"context"
	"os"
	"os/exec"
	"testing"
)

func TestIsProcessAliveReportsTheRunningProcess(t *testing.T) {
	if !IsProcessAlive(os.Getpid()) {
		t.Fatal("IsProcessAlive(os.Getpid()) = false, want true")
	}
}

func TestIsProcessAliveReportsAnExitedProcessAsNotAlive(t *testing.T) {
	cmd := exec.CommandContext(context.Background(), "true")
	if err := cmd.Run(); err != nil {
		t.Fatalf("run short-lived process: %v", err)
	}
	if IsProcessAlive(cmd.Process.Pid) {
		t.Fatalf("IsProcessAlive(%d) = true right after it exited, want false", cmd.Process.Pid)
	}
}
