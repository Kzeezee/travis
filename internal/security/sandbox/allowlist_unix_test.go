//go:build linux || darwin

package sandbox_test

import (
	"bytes"
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Kzeezee/travis/internal/security/sandbox"
)

// The allowlist must actually grant access, or every escape assertion could pass on a sandbox that blocks everything.
func TestSandboxGrantsTheAllowlist(t *testing.T) {
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}

	t.Run("write inside scratch", func(t *testing.T) {
		spec := buildProbeSpec(t, self, "write", "")
		spec.Argv[1] = filepath.Join(spec.WritePaths[0], "allowed.txt")
		assertProbeEscaped(t, spec)
	})

	t.Run("read of an allowed directory", func(t *testing.T) {
		allowed := t.TempDir()
		target := filepath.Join(allowed, "allowed.txt")
		if err := os.WriteFile(target, []byte("allowed"), 0o600); err != nil {
			t.Fatal(err)
		}
		spec := buildProbeSpec(t, self, "read", target)
		spec.ReadPaths = append(spec.ReadPaths, allowed)
		assertProbeEscaped(t, spec)
	})

	// Package managers download into a temp folder and rename into their cache (TRA-62).
	t.Run("rename between folders inside scratch", func(t *testing.T) {
		spec := buildProbeSpec(t, self, "rename", "")
		from, to := createRenameSource(t, spec.WritePaths[0]), filepath.Join(spec.WritePaths[0], "cache", "f")
		if err := os.Mkdir(filepath.Dir(to), 0o700); err != nil {
			t.Fatal(err)
		}
		spec.Argv[1] = from + string(os.PathListSeparator) + to
		assertProbeEscaped(t, spec)
	})

	t.Run("network connect with AllowNetwork", func(t *testing.T) {
		spec := buildProbeSpec(t, self, "connect", listenOnLoopbackTCP(t))
		spec.AllowNetwork = true
		assertProbeEscaped(t, spec)
	})

	// localhost resolves from the hosts file, so only a real name proves lookups reach the resolver.
	t.Run("hostname lookup with AllowNetwork", func(t *testing.T) {
		const hostname = "example.com"
		if _, err := net.DefaultResolver.LookupHost(t.Context(), hostname); err != nil {
			t.Skipf("no DNS outside the sandbox: %v", err)
		}
		spec := buildProbeSpec(t, self, "resolve", hostname)
		spec.AllowNetwork = true
		assertProbeEscaped(t, spec)
	})
}

// Granting rename inside scratch must not let a file be moved out of it.
func TestSandboxDeniesRenameOutOfScratch(t *testing.T) {
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	spec := buildProbeSpec(t, self, "rename", "")
	to := filepath.Join(t.TempDir(), "escaped")
	spec.Argv[1] = createRenameSource(t, spec.WritePaths[0]) + string(os.PathListSeparator) + to
	assertProbeDenied(t, spec)
	if _, err := os.Stat(to); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("file moved out of scratch after a denied rename: %v", err)
	}
}

// createRenameSource writes scratch/tmp/f, the file a rename probe moves.
func createRenameSource(t *testing.T, scratch string) string {
	t.Helper()
	from := filepath.Join(scratch, "tmp", "f")
	if err := os.Mkdir(filepath.Dir(from), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(from, []byte("download"), 0o600); err != nil {
		t.Fatal(err)
	}
	return from
}

// AllowNetwork means IP only; a Unix socket would reach local services such as docker.sock or ssh-agent.
func TestSandboxDeniesUnixSocketsWithAllowNetwork(t *testing.T) {
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	spec := buildProbeSpec(t, self, "connect-unix", listenOnUnixSocket(t))
	if _, tier, _ := sandbox.BuildCommand(t.Context(), spec); tier == sandbox.TierLandlockNoNet {
		t.Skipf("%s tier cannot deny Unix sockets", tier)
	}
	spec.AllowNetwork = true
	assertProbeDenied(t, spec)
}

func assertProbeEscaped(t *testing.T, spec sandbox.Spec) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	cmd, _, err := sandbox.BuildCommand(ctx, spec)
	if err != nil {
		t.Fatalf("BuildCommand: %v", err)
	}
	var output bytes.Buffer
	cmd.Stdout, cmd.Stderr = &output, &output
	if err := cmd.Run(); err != nil {
		t.Fatalf("allowed probe failed (%v):\n%s", err, output.String())
	}
}
