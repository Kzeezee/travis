package sandbox_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/Kzeezee/travis/internal/core"
	"github.com/Kzeezee/travis/internal/security/sandbox"
)

// escapeProbeEnv turns this test binary into a probe that tries one escape inside the sandbox.
const escapeProbeEnv = "TRAVIS_SANDBOX_ESCAPE_PROBE"

// Probe exit codes: escapedExitCode means the sandbox let the probe through; any code other than
// these two means the harness broke, so a crashing probe never passes as a denial.
const (
	escapedExitCode = 0
	deniedExitCode  = 10
)

func TestMain(m *testing.M) {
	if len(os.Args) > 1 && os.Args[1] == sandbox.LauncherArg {
		if err := sandbox.RunLauncher(os.Args[2:]); err != nil {
			fmt.Fprintln(os.Stderr, "sandbox launcher:", err)
			os.Exit(1)
		}
		os.Exit(0)
	}
	if probe := os.Getenv(escapeProbeEnv); probe != "" {
		os.Exit(runEscapeProbe(probe, os.Args[1:]))
	}
	os.Exit(m.Run())
}

func runEscapeProbe(probe string, args []string) int {
	if len(args) != 1 {
		fmt.Fprintf(os.Stderr, "escape probe %q wants 1 argument, got %d\n", probe, len(args))
		return 2
	}
	target := args[0]
	var err error
	switch probe {
	case "write":
		err = os.WriteFile(target, []byte("escaped"), 0o600)
	case "read":
		_, err = os.ReadFile(target)
	case "connect":
		dialer := net.Dialer{Timeout: 2 * time.Second}
		var conn net.Conn
		if conn, err = dialer.DialContext(context.Background(), "tcp", target); err == nil {
			_ = conn.Close()
		}
	case "spawn-heartbeat":
		return spawnHeartbeatAndWait(target)
	case "heartbeat":
		return writeHeartbeat(target)
	default:
		fmt.Fprintf(os.Stderr, "escape probe: unknown probe %q\n", probe)
		return 2
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "escape probe denied:", err)
		return deniedExitCode
	}
	return escapedExitCode
}

// spawnHeartbeatAndWait starts a grandchild that keeps writing, then idles until it is killed.
func spawnHeartbeatAndWait(heartbeatPath string) int {
	self, err := os.Executable()
	if err != nil {
		return 2
	}
	grandchild := exec.Command(self, heartbeatPath) //nolint:noctx // the probe re-runs itself; the sandbox, not a context, must kill it
	grandchild.Env = append(os.Environ(), escapeProbeEnv+"=heartbeat")
	if err := grandchild.Start(); err != nil {
		fmt.Fprintln(os.Stderr, "escape probe: start heartbeat:", err)
		return 2
	}
	time.Sleep(time.Minute)
	return 2
}

// writeHeartbeat appends a byte every few milliseconds; it stops on its own so a failed test leaks nothing for long.
func writeHeartbeat(heartbeatPath string) int {
	heartbeat, err := os.OpenFile(heartbeatPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return 2
	}
	defer heartbeat.Close()
	for deadline := time.Now().Add(30 * time.Second); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
		if _, err := heartbeat.Write([]byte{'.'}); err != nil {
			return 2
		}
	}
	return 0
}

// TestSandboxEscape is the escape test every OS backend must pass (4B to 4D); without a backend it reports ErrUnsupported.
func TestSandboxEscape(t *testing.T) {
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := sandbox.BuildCommand(t.Context(), buildProbeSpec(t, self, "write", t.TempDir())); errors.Is(err, core.ErrUnsupported) {
		t.Skipf("sandbox escape test on %s: %v", runtime.GOOS, err)
	} else if err != nil {
		t.Fatalf("BuildCommand: %v", err)
	}

	t.Run("write outside scratch is denied", func(t *testing.T) {
		outside := filepath.Join(t.TempDir(), "escaped.txt")
		assertProbeDenied(t, buildProbeSpec(t, self, "write", outside))
		if _, err := os.Stat(outside); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("file outside scratch exists after a denied write: %v", err)
		}
	})

	t.Run("read of a denied directory is denied", func(t *testing.T) {
		secret := filepath.Join(t.TempDir(), "secret.txt")
		if err := os.WriteFile(secret, []byte("secret"), 0o600); err != nil {
			t.Fatal(err)
		}
		assertProbeDenied(t, buildProbeSpec(t, self, "read", secret))
	})

	t.Run("network connect is denied without AllowNetwork", func(t *testing.T) {
		var listenConfig net.ListenConfig
		listener, err := listenConfig.Listen(t.Context(), "tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = listener.Close() })
		assertProbeDenied(t, buildProbeSpec(t, self, "connect", listener.Addr().String()))
	})

	t.Run("no process in the tree survives cancellation", func(t *testing.T) {
		spec := buildProbeSpec(t, self, "spawn-heartbeat", "")
		heartbeatPath := filepath.Join(spec.WritePaths[0], "heartbeat")
		spec.Argv[1] = heartbeatPath
		assertTreeDiesOnCancel(t, spec, heartbeatPath)
	})
}

// buildProbeSpec confines a probe to a fresh scratch directory, with read access only to the probe binary.
func buildProbeSpec(t *testing.T, self, probe, target string) sandbox.Spec {
	t.Helper()
	scratch := t.TempDir()
	return sandbox.Spec{
		Launcher:   self,
		Argv:       []string{self, target},
		Env:        append(os.Environ(), escapeProbeEnv+"="+probe),
		Dir:        scratch,
		ReadPaths:  []string{filepath.Dir(self)},
		WritePaths: []string{scratch},
	}
}

func assertProbeDenied(t *testing.T, spec sandbox.Spec) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	cmd, tier, err := sandbox.BuildCommand(ctx, spec)
	if err != nil {
		t.Fatalf("BuildCommand: %v", err)
	}
	var output bytes.Buffer
	cmd.Stdout, cmd.Stderr = &output, &output
	err = cmd.Run()
	var exitErr *exec.ExitError
	switch {
	case err == nil:
		t.Fatalf("tier %s let the probe escape:\n%s", tier, output.String())
	case !errors.As(err, &exitErr) || exitErr.ExitCode() != deniedExitCode:
		t.Fatalf("tier %s probe failed without reaching its escape (%v):\n%s", tier, err, output.String())
	}
}

func assertTreeDiesOnCancel(t *testing.T, spec sandbox.Spec, heartbeatPath string) {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	cmd, tier, err := sandbox.BuildCommand(ctx, spec)
	if err != nil {
		t.Fatalf("BuildCommand: %v", err)
	}
	// A surviving grandchild would hold any output pipe open, so Wait must not block on it.
	cmd.WaitDelay = time.Second
	if err := cmd.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	if !waitForHeartbeat(heartbeatPath, 10*time.Second) {
		cancel()
		_ = cmd.Wait()
		t.Fatalf("tier %s: the grandchild never started its heartbeat", tier)
	}
	cancel()
	_ = cmd.Wait()

	before := reportFileSize(heartbeatPath)
	time.Sleep(300 * time.Millisecond)
	if after := reportFileSize(heartbeatPath); after != before {
		t.Fatalf("tier %s: a grandchild kept writing after cancellation (%d -> %d bytes)", tier, before, after)
	}
}

func waitForHeartbeat(heartbeatPath string, timeout time.Duration) bool {
	for deadline := time.Now().Add(timeout); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
		if reportFileSize(heartbeatPath) > 0 {
			return true
		}
	}
	return false
}

func reportFileSize(path string) int64 {
	info, err := os.Stat(path)
	if err != nil {
		return 0
	}
	return info.Size()
}
