package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
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
	brokerPath := filepath.Join(filepath.Dir(testBinary), brokerName)
	brokerBuild := exec.CommandContext(context.Background(), goTool, "build", "-o", brokerPath, "github.com/Kzeezee/travis/cmd/travis-broker")
	if output, err := brokerBuild.CombinedOutput(); err != nil {
		return fmt.Errorf("build travis-broker: %w\n%s", err, output)
	}
	// What `make build` stamps into a real travis (R-23).
	brokerBytes, err := os.ReadFile(brokerPath)
	if err != nil {
		return err
	}
	digest := sha256.Sum256(brokerBytes)
	brokerSHA256 = hex.EncodeToString(digest[:])
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

// testSessionID names the session log every test broker keeps.
const testSessionID core.ID = "test-session"

// startTestSessionBroker runs the real broker process over workspace for the rest of the test.
// The broker finds its state directory the way production does, from the environment, so the
// test points TRAVIS_HOME at a scratch directory and reports the state directory under it.
func startTestSessionBroker(t *testing.T, paths config.PathSet, diagnostics *strings.Builder, client broker.RemoteOptions) (*broker.RemoteBroker, string) {
	t.Helper()
	travisHome := t.TempDir()
	t.Setenv("TRAVIS_HOME", travisHome)
	sessionCfg := config.SessionCfg{SnapshotMaxBytes: 1 << 20}
	sessionBroker, err := startSessionBroker(paths, sessionStart{ID: testSessionID}, "default", sessionCfg, diagnostics, client)
	if err != nil {
		t.Fatalf("start session broker: %v (diagnostics: %s)", err, diagnostics)
	}
	t.Cleanup(func() {
		if err := sessionBroker.Close(); err != nil {
			t.Errorf("session broker: %v", err)
		}
	})
	return sessionBroker.Remote, filepath.Join(travisHome, "state")
}

// startTestSessionBrokerProcess is startTestSessionBroker for tests that need to close the
// broker themselves and then observe what its process left behind, so it does not also
// register an automatic Close on cleanup that would race the test's own.
func startTestSessionBrokerProcess(t *testing.T, paths config.PathSet, diagnostics *strings.Builder, client broker.RemoteOptions) (*broker.SessionBroker, string) {
	t.Helper()
	travisHome := t.TempDir()
	t.Setenv("TRAVIS_HOME", travisHome)
	sessionCfg := config.SessionCfg{SnapshotMaxBytes: 1 << 20}
	sessionBroker, err := startSessionBroker(paths, sessionStart{ID: testSessionID}, "default", sessionCfg, diagnostics, client)
	if err != nil {
		t.Fatalf("start session broker: %v (diagnostics: %s)", err, diagnostics)
	}
	return sessionBroker, filepath.Join(travisHome, "state")
}

// The broker process, not the tool, refuses a deny-listed file inside the workspace, and the
// refusal is published as PolicyDenied (D-29); the Phase 1 LocalBroker would have read it.
func TestFileReadThroughTheBrokerProcessIsRefusedByPolicy(t *testing.T) {
	workspace := t.TempDir()
	if err := os.WriteFile(filepath.Join(workspace, ".env"), []byte("TOKEN=secret\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workspace, "notes.md"), []byte("hello\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var diagnostics strings.Builder
	bus := buildSessionBus(io.Discard)
	events, _ := bus.Subscribe(nil)
	fileBroker, _ := startTestSessionBroker(t, config.PathSet{WorkspaceDir: workspace, RuntimeDir: buildShortRuntimeDir(t)}, &diagnostics, broker.RemoteOptions{Events: bus})
	fileRead := builtin.BuildFileReadTool(fileBroker, workspace, defaultMaxReadBytes, nil)

	refused, err := fileRead.Invoke(core.WithToolCallID(context.Background(), "call-env"), json.RawMessage(`{"path":".env"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !refused.IsError || strings.Contains(refused.Content, "TOKEN=secret") {
		t.Fatalf("fs_read .env = %+v, want a refusal without the secret", refused)
	}
	bus.Close()
	var denials []core.PolicyDenied
	for event := range events {
		if denied, isDenial := event.(core.PolicyDenied); isDenial {
			denials = append(denials, denied)
		}
	}
	if len(denials) == 0 || denials[0].CallID != "call-env" || denials[0].Capability.Kind != core.CapabilityFSRead || !strings.Contains(denials[0].Rule, "deny") {
		t.Fatalf("PolicyDenied events = %+v, want call-env's fs_read denial naming its rule", denials)
	}

	allowed, err := fileRead.Invoke(context.Background(), json.RawMessage(`{"path":"notes.md"}`))
	if err != nil || allowed.IsError || !strings.Contains(allowed.Content, "hello") {
		t.Fatalf("fs_read notes.md = %+v, %v; want its content", allowed, err)
	}
}

// A session nobody ever said anything in is deleted the moment its broker exits cleanly
// (TRA-86), rather than left for retention to notice, possibly days later.
func TestEmptySessionIsDeletedOnACleanBrokerExit(t *testing.T) {
	var diagnostics strings.Builder
	sessionBroker, stateDir := startTestSessionBrokerProcess(t, config.PathSet{WorkspaceDir: t.TempDir(), RuntimeDir: buildShortRuntimeDir(t)}, &diagnostics, broker.RemoteOptions{})

	if err := sessionBroker.Close(); err != nil {
		t.Fatalf("session broker close: %v (diagnostics: %s)", err, diagnostics.String())
	}

	sessionsDir := filepath.Join(stateDir, "sessions")
	for _, suffix := range []string{".jsonl", ".lock"} {
		if _, err := os.Stat(filepath.Join(sessionsDir, string(testSessionID)+suffix)); !os.IsNotExist(err) {
			t.Fatalf("test-session%s still exists after a clean exit with nothing said: %v (diagnostics: %s)", suffix, err, diagnostics.String())
		}
	}
}

// A session that did hold a message is left alone on a clean exit: only emptiness triggers
// TRA-86's cleanup, never age or content.
func TestNonEmptySessionSurvivesACleanBrokerExit(t *testing.T) {
	workspace := t.TempDir()
	var diagnostics strings.Builder
	sessionBroker, stateDir := startTestSessionBrokerProcess(t, config.PathSet{WorkspaceDir: workspace, RuntimeDir: buildShortRuntimeDir(t)}, &diagnostics, broker.RemoteOptions{})
	fileWrite := builtin.BuildFileWriteTool(sessionBroker.Remote, workspace, defaultMaxReadBytes, "")

	turnCtx := core.WithTurnID(context.Background(), "turn-1")
	written, err := fileWrite.Invoke(turnCtx, json.RawMessage(`{"path":"notes.md","content":"hi"}`))
	if err != nil || written.IsError {
		t.Fatalf("fs_write notes.md = %+v, %v", written, err)
	}

	if err := sessionBroker.Close(); err != nil {
		t.Fatalf("session broker close: %v (diagnostics: %s)", err, diagnostics.String())
	}

	if _, err := os.Stat(filepath.Join(stateDir, "sessions", string(testSessionID)+".jsonl")); err != nil {
		t.Fatalf("session log removed although it held an entry: %v", err)
	}
}
