package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/Kzeezee/travis/internal/config"
	"github.com/Kzeezee/travis/internal/core"
	"github.com/Kzeezee/travis/internal/security/broker"
	"github.com/Kzeezee/travis/internal/tools/builtin"
)

// TestMain builds the real travis-broker beside the test binary, which is exactly where
// ResolveBrokerExecutable looks for it in production.
func TestMain(m *testing.M) {
	if err := buildBrokerBesideTestBinary(); err != nil {
		fmt.Fprintln(os.Stderr, "cmd/travis tests:", err)
		os.Exit(1)
	}
	os.Exit(m.Run())
}

func buildBrokerBesideTestBinary() error {
	testBinary, err := os.Executable()
	if err != nil {
		return err
	}
	brokerName := "travis-broker"
	if runtime.GOOS == "windows" {
		brokerName += ".exe"
	}
	goTool, err := exec.LookPath("go")
	if err != nil {
		return fmt.Errorf("the go tool is needed to build travis-broker for these tests: %w", err)
	}
	brokerBuild := exec.CommandContext(context.Background(), goTool, "build", "-o", filepath.Join(filepath.Dir(testBinary), brokerName), "github.com/Kzeezee/travis/cmd/travis-broker")
	if output, err := brokerBuild.CombinedOutput(); err != nil {
		return fmt.Errorf("build travis-broker: %w\n%s", err, output)
	}
	return nil
}

// buildShortRuntimeDir keeps socket paths under macOS's 104-byte limit, which t.TempDir() names
// alone can approach.
func buildShortRuntimeDir(t *testing.T) string {
	t.Helper()
	runtimeDir, err := os.MkdirTemp("", "tb")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(runtimeDir) })
	return runtimeDir
}

// skipWithoutBrokerTransport skips before a test arms checks, such as a cassette's
// every-response-was-used assertion, that a later skip would trip in cleanup.
func skipWithoutBrokerTransport(t *testing.T) {
	t.Helper()
	_, removeEndpoint, err := broker.CreateSessionEndpoint(buildShortRuntimeDir(t))
	if errors.Is(err, core.ErrUnsupported) {
		t.Skipf("no broker transport on this OS yet: %v", err)
	}
	if err != nil {
		t.Fatal(err)
	}
	removeEndpoint()
}

// startTestSessionBroker runs the real broker process over workspace for the rest of the test.
// It skips where the broker transport is not built yet, since travis cannot run a chat there.
func startTestSessionBroker(t *testing.T, paths config.PathSet, diagnostics *strings.Builder) core.Broker {
	t.Helper()
	sessionBroker, err := startSessionBroker(paths, diagnostics)
	if errors.Is(err, core.ErrUnsupported) {
		t.Skipf("no broker transport on this OS yet: %v", err)
	}
	if err != nil {
		t.Fatalf("start session broker: %v (diagnostics: %s)", err, diagnostics)
	}
	t.Cleanup(func() {
		if err := sessionBroker.Close(); err != nil {
			t.Errorf("session broker: %v", err)
		}
	})
	return sessionBroker.Remote
}

// The broker process, not the tool, refuses a deny-listed file inside the workspace; the
// Phase 1 LocalBroker would have read it.
func TestFileReadThroughTheBrokerProcessIsRefusedByPolicy(t *testing.T) {
	workspace := t.TempDir()
	if err := os.WriteFile(filepath.Join(workspace, ".env"), []byte("TOKEN=secret\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workspace, "notes.md"), []byte("hello\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var diagnostics strings.Builder
	fileBroker := startTestSessionBroker(t, config.PathSet{WorkspaceDir: workspace, RuntimeDir: buildShortRuntimeDir(t)}, &diagnostics)
	fileRead := builtin.BuildFileReadTool(fileBroker, workspace, defaultMaxReadBytes)

	refused, err := fileRead.Invoke(context.Background(), json.RawMessage(`{"path":".env"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !refused.IsError || strings.Contains(refused.Content, "TOKEN=secret") {
		t.Fatalf("fs_read .env = %+v, want a refusal without the secret", refused)
	}

	allowed, err := fileRead.Invoke(context.Background(), json.RawMessage(`{"path":"notes.md"}`))
	if err != nil || allowed.IsError || !strings.Contains(allowed.Content, "hello") {
		t.Fatalf("fs_read notes.md = %+v, %v; want its content", allowed, err)
	}
}
