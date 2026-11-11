package broker

import (
	"context"
	"os"
	"os/exec"
	"testing"
)

func TestIsProcessAliveReportsTheRunningProcess(t *testing.T) {
	if !isProcessAlive(os.Getpid()) {
		t.Fatal("isProcessAlive(os.Getpid()) = false, want true")
	}
}

func TestIsProcessAliveReportsAnExitedProcessAsNotAlive(t *testing.T) {
	cmd := exec.CommandContext(context.Background(), "cmd", "/C", "exit 0")
	if err := cmd.Run(); err != nil {
		t.Fatalf("run short-lived process: %v", err)
	}
	if isProcessAlive(cmd.Process.Pid) {
		t.Fatalf("isProcessAlive(%d) = true right after it exited, want false", cmd.Process.Pid)
	}
}
