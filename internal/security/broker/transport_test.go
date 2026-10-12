//go:build linux || darwin || windows

package broker_test

import (
	"context"
	"errors"
	"os"
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

func TestCreateSessionEndpointNamesEachSessionApart(t *testing.T) {
	runtimeDir := buildShortRuntimeDir(t)
	if first, second := createEndpoint(t, runtimeDir), createEndpoint(t, runtimeDir); first == second {
		t.Fatalf("two sessions share endpoint %q", first)
	}
}

func TestTransportServesItsVerifiedPeer(t *testing.T) {
	workspace := t.TempDir()
	writeWorkspaceFile(t, workspace, "notes/today.md", "hi")
	endpoint := createEndpoint(t, buildShortRuntimeDir(t))
	// Both ends live in this test process, so each side's expected peer is this pid.
	serveInBackground(t, endpoint, os.Getpid(), broker.BuildLocalBroker(workspace, core.SystemClock{}))

	remote, err := broker.ConnectRemoteBroker(context.Background(), endpoint, os.Getpid(), broker.RemoteOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer remote.Close()
	info, err := remote.DescribePath(context.Background(), "notes/today.md")
	if err != nil || info.WorkspacePath != "notes/today.md" {
		t.Fatalf("DescribePath over the transport = %+v, %v", info, err)
	}
}

func TestTransportRefusesAClientThatIsNotTheExpectedProcess(t *testing.T) {
	endpoint := createEndpoint(t, buildShortRuntimeDir(t))
	served := serveInBackground(t, endpoint, os.Getpid()+1, broker.BuildLocalBroker(t.TempDir(), core.SystemClock{}))

	remote, err := broker.ConnectRemoteBroker(context.Background(), endpoint, os.Getpid(), broker.RemoteOptions{})
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

func TestTransportRefusesABrokerThatIsNotTheExpectedProcess(t *testing.T) {
	endpoint := createEndpoint(t, buildShortRuntimeDir(t))
	serveInBackground(t, endpoint, os.Getpid(), broker.BuildLocalBroker(t.TempDir(), core.SystemClock{}))

	if _, err := broker.ConnectRemoteBroker(context.Background(), endpoint, os.Getpid()+1, broker.RemoteOptions{}); !errors.Is(err, core.ErrDenied) {
		t.Fatalf("ConnectRemoteBroker(wrong pid) error = %v, want ErrDenied", err)
	}
}

func TestTransportAcceptsOnlyOneClientPerSession(t *testing.T) {
	endpoint := createEndpoint(t, buildShortRuntimeDir(t))
	serveInBackground(t, endpoint, os.Getpid(), broker.BuildLocalBroker(t.TempDir(), core.SystemClock{}))

	first, err := broker.ConnectRemoteBroker(context.Background(), endpoint, os.Getpid(), broker.RemoteOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	if _, err := first.DescribePath(context.Background(), "."); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if second, err := broker.ConnectRemoteBroker(ctx, endpoint, os.Getpid(), broker.RemoteOptions{}); err == nil {
		_ = second.Close()
		t.Fatal("a second client connected to a session that already has one")
	}
}
