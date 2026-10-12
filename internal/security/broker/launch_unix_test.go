//go:build linux || darwin

package broker_test

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Kzeezee/travis/internal/core"
	"github.com/Kzeezee/travis/internal/security/broker"
)

// helperBrokerModeEnv turns this test binary into a stand-in travis-broker for launch tests.
const helperBrokerModeEnv = "TRAVIS_BROKER_TEST_HELPER"

func TestMain(m *testing.M) {
	if helperMode := os.Getenv(helperBrokerModeEnv); helperMode != "" {
		os.Exit(runHelperBroker(helperMode, os.Args[1:]))
	}
	os.Exit(m.Run())
}

// runHelperBroker behaves like travis-broker for "serve", or fails on purpose for the others.
func runHelperBroker(helperMode string, args []string) int {
	switch helperMode {
	case "exit-early":
		fmt.Fprintln(os.Stderr, "helper broker: exiting before ready on purpose")
		return 3
	case "never-ready":
		// Not select{}: the runtime would kill an all-goroutines-blocked process as deadlocked.
		time.Sleep(time.Hour)
		return 0
	case "serve":
	default:
		fmt.Fprintf(os.Stderr, "helper broker: unknown mode %q\n", helperMode)
		return 2
	}

	flags := flag.NewFlagSet("helper-broker", flag.ContinueOnError)
	endpoint := flags.String("endpoint", "", "")
	clientPID := flags.Int("client-pid", 0, "")
	flags.String("profile", "", "")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	workspace, err := os.Getwd()
	if err != nil {
		return 2
	}
	sessionListener, err := broker.ListenForSession(broker.SessionEndpoint(*endpoint))
	if err != nil {
		fmt.Fprintln(os.Stderr, "helper broker:", err)
		return 1
	}
	fmt.Println(broker.BrokerReadyLine)
	if err := sessionListener.ServeOneClient(context.Background(), *clientPID, broker.BuildLocalBroker(workspace, core.SystemClock{})); err != nil {
		fmt.Fprintln(os.Stderr, "helper broker:", err)
		return 1
	}
	return 0
}

func buildHelperLaunch(t *testing.T, helperMode string) broker.SessionLaunch {
	t.Helper()
	t.Setenv(helperBrokerModeEnv, helperMode)
	testBinary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	return broker.SessionLaunch{
		Executable:   testBinary,
		WorkspaceDir: t.TempDir(),
		RuntimeDir:   buildShortRuntimeDir(t),
		Profile:      "default",
		Diagnostics:  &strings.Builder{},
	}
}

func requireNoEndpointLeft(t *testing.T, runtimeDir string) {
	t.Helper()
	if entries, err := os.ReadDir(runtimeDir); err != nil || len(entries) != 0 {
		t.Fatalf("runtime dir holds %v (%v), want nothing left behind", entries, err)
	}
}

func TestStartSessionBrokerConnectsToItsChildAndShutsItDown(t *testing.T) {
	launch := buildHelperLaunch(t, "serve")
	writeWorkspaceFile(t, launch.WorkspaceDir, "notes/today.md", "hi")

	sessionBroker, err := broker.StartSessionBroker(context.Background(), launch)
	if err != nil {
		t.Fatalf("StartSessionBroker: %v (diagnostics: %s)", err, launch.Diagnostics)
	}
	info, err := sessionBroker.Remote.DescribePath(context.Background(), "notes/today.md")
	if err != nil || info.WorkspacePath != "notes/today.md" {
		t.Fatalf("DescribePath through the child = %+v, %v", info, err)
	}

	if err := sessionBroker.Close(); err != nil {
		t.Fatalf("Close = %v, want a clean exit (diagnostics: %s)", err, launch.Diagnostics)
	}
	requireNoEndpointLeft(t, launch.RuntimeDir)
	if _, err := sessionBroker.Remote.DescribePath(context.Background(), "."); err == nil {
		t.Fatal("the broker connection still works after Close")
	}
}

func TestStartSessionBrokerReportsABrokerThatExitsBeforeReady(t *testing.T) {
	launch := buildHelperLaunch(t, "exit-early")

	_, err := broker.StartSessionBroker(context.Background(), launch)
	if err == nil || !strings.Contains(err.Error(), "did not become ready") {
		t.Fatalf("StartSessionBroker error = %v, want did not become ready", err)
	}
	if !strings.Contains(fmt.Sprint(launch.Diagnostics), "exiting before ready on purpose") {
		t.Fatalf("diagnostics = %q, want the broker's own reason", launch.Diagnostics)
	}
	requireNoEndpointLeft(t, launch.RuntimeDir)
}

func TestStartSessionBrokerGivesUpOnABrokerThatNeverBecomesReady(t *testing.T) {
	launch := buildHelperLaunch(t, "never-ready")
	launch.ReadyTimeout = 200 * time.Millisecond

	_, err := broker.StartSessionBroker(context.Background(), launch)
	if err == nil || !strings.Contains(err.Error(), "no ready line") {
		t.Fatalf("StartSessionBroker error = %v, want a ready timeout", err)
	}
	requireNoEndpointLeft(t, launch.RuntimeDir)
}
