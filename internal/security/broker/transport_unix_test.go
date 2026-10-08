//go:build linux || darwin

package broker_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Kzeezee/travis/internal/core"
	"github.com/Kzeezee/travis/internal/security/broker"
)

// buildShortRuntimeDir is a runtime dir short enough for a socket path: t.TempDir() names on
// macOS alone approach the 104-byte limit.
func buildShortRuntimeDir(t *testing.T) string {
	t.Helper()
	runtimeDir, err := os.MkdirTemp("", "tb")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(runtimeDir) })
	return runtimeDir
}

func createEndpoint(t *testing.T, runtimeDir string) broker.SessionEndpoint {
	t.Helper()
	endpoint, removeEndpoint, err := broker.CreateSessionEndpoint(runtimeDir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(removeEndpoint)
	return endpoint
}

// serveInBackground listens on endpoint and serves one client, which must be clientPID.
func serveInBackground(t *testing.T, endpoint broker.SessionEndpoint, clientPID int, effects core.Broker) <-chan error {
	t.Helper()
	sessionListener, err := broker.ListenForSession(endpoint)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	served := make(chan error, 1)
	go func() { served <- sessionListener.ServeOneClient(ctx, clientPID, effects) }()
	t.Cleanup(cancel)
	return served
}

func TestCreateSessionEndpointMakesAPrivateDirectoryPerSession(t *testing.T) {
	runtimeDir := buildShortRuntimeDir(t)
	first, removeFirst, err := broker.CreateSessionEndpoint(runtimeDir)
	if err != nil {
		t.Fatal(err)
	}
	second := createEndpoint(t, runtimeDir)
	if first.Address == second.Address {
		t.Fatalf("two sessions share endpoint %q", first.Address)
	}

	sessionDir := filepath.Dir(first.Address)
	info, err := os.Stat(sessionDir)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o700 {
		t.Fatalf("session directory %q has mode %v, want 0700", sessionDir, perm)
	}
	removeFirst()
	if _, err := os.Stat(sessionDir); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("session directory still exists after removal: %v", err)
	}
}

func TestCreateSessionEndpointRefusesSocketPathsOverTheOSLimit(t *testing.T) {
	runtimeDir := filepath.Join(buildShortRuntimeDir(t), strings.Repeat("d", 100))
	if _, _, err := broker.CreateSessionEndpoint(runtimeDir); !errors.Is(err, core.ErrInvalid) {
		t.Fatalf("CreateSessionEndpoint(long dir) error = %v, want ErrInvalid", err)
	}
	if entries, _ := os.ReadDir(runtimeDir); len(entries) != 0 {
		t.Fatalf("a refused endpoint left %v behind", entries)
	}
}

func TestUnixTransportServesItsVerifiedPeer(t *testing.T) {
	workspace := t.TempDir()
	writeWorkspaceFile(t, workspace, "notes/today.md", "hi")
	endpoint := createEndpoint(t, buildShortRuntimeDir(t))
	// Both ends live in this test process, so each side's expected peer is this pid.
	serveInBackground(t, endpoint, os.Getpid(), broker.BuildLocalBroker(workspace))

	remote, err := broker.ConnectRemoteBroker(context.Background(), endpoint, os.Getpid())
	if err != nil {
		t.Fatal(err)
	}
	defer remote.Close()
	info, err := remote.DescribePath(context.Background(), "notes/today.md")
	if err != nil || info.WorkspacePath != "notes/today.md" {
		t.Fatalf("DescribePath over the socket = %+v, %v", info, err)
	}
}

func TestUnixTransportRefusesAClientThatIsNotTheExpectedProcess(t *testing.T) {
	endpoint := createEndpoint(t, buildShortRuntimeDir(t))
	served := serveInBackground(t, endpoint, os.Getpid()+1, broker.BuildLocalBroker(t.TempDir()))

	remote, err := broker.ConnectRemoteBroker(context.Background(), endpoint, os.Getpid())
	if err != nil {
		t.Fatal(err)
	}
	defer remote.Close()
	if err := <-served; !errors.Is(err, core.ErrDenied) {
		t.Fatalf("ServeOneClient(wrong pid) = %v, want ErrDenied", err)
	}
	if _, err := remote.DescribePath(context.Background(), "."); err == nil {
		t.Fatal("a refused client could still use the broker")
	}
}

func TestUnixTransportRefusesABrokerThatIsNotTheExpectedProcess(t *testing.T) {
	endpoint := createEndpoint(t, buildShortRuntimeDir(t))
	serveInBackground(t, endpoint, os.Getpid(), broker.BuildLocalBroker(t.TempDir()))

	if _, err := broker.ConnectRemoteBroker(context.Background(), endpoint, os.Getpid()+1); !errors.Is(err, core.ErrDenied) {
		t.Fatalf("ConnectRemoteBroker(wrong pid) error = %v, want ErrDenied", err)
	}
}

func TestUnixTransportAcceptsOnlyOneClientPerSession(t *testing.T) {
	endpoint := createEndpoint(t, buildShortRuntimeDir(t))
	serveInBackground(t, endpoint, os.Getpid(), broker.BuildLocalBroker(t.TempDir()))

	first, err := broker.ConnectRemoteBroker(context.Background(), endpoint, os.Getpid())
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	if _, err := first.DescribePath(context.Background(), "."); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if second, err := broker.ConnectRemoteBroker(ctx, endpoint, os.Getpid()); err == nil {
		_ = second.Close()
		t.Fatal("a second client connected to a session that already has one")
	}
}
