package sandbox_test

import (
	"bytes"
	"context"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Kzeezee/travis/internal/security/sandbox"
)

func TestSandboxReportsSeatbeltTier(t *testing.T) {
	_, tier, err := sandbox.BuildCommand(t.Context(), buildValidSpec(t))
	if err != nil || tier != sandbox.TierSeatbelt {
		t.Fatalf("BuildCommand tier = %q, %v; want %q", tier, err, sandbox.TierSeatbelt)
	}
}

// The allowlist must actually grant access, or every escape assertion could pass on a profile that blocks everything.
func TestSeatbeltProfileGrantsTheAllowlist(t *testing.T) {
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

	t.Run("network connect with AllowNetwork", func(t *testing.T) {
		var listenConfig net.ListenConfig
		listener, err := listenConfig.Listen(t.Context(), "tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = listener.Close() })
		spec := buildProbeSpec(t, self, "connect", listener.Addr().String())
		spec.AllowNetwork = true
		assertProbeEscaped(t, spec)
	})

	// localhost resolves from the hosts file, so only a real name proves lookups reach mDNSResponder.
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

// AllowNetwork means IP only; a Unix socket would reach local services such as docker.sock or ssh-agent.
func TestSeatbeltDeniesUnixSocketsWithAllowNetwork(t *testing.T) {
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	// t.TempDir paths can pass the ~104-byte Unix socket path limit, so the socket gets a shorter directory.
	socketDir, err := os.MkdirTemp("", "sb")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(socketDir) })
	var listenConfig net.ListenConfig
	listener, err := listenConfig.Listen(t.Context(), "unix", filepath.Join(socketDir, "s"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	spec := buildProbeSpec(t, self, "connect-unix", listener.Addr().String())
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
