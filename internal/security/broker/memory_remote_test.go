package broker_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Kzeezee/travis/internal/config"
	"github.com/Kzeezee/travis/internal/core"
	"github.com/Kzeezee/travis/internal/security/broker"
	"github.com/Kzeezee/travis/internal/state/memory"
)

func buildMemoryBrokerForTest(t *testing.T, profile string) (*broker.PolicyEnforcedBroker, string) {
	t.Helper()
	root := t.TempDir()
	workspace, state := filepath.Join(root, "workspace"), filepath.Join(root, "state")
	if err := os.MkdirAll(workspace, 0o700); err != nil {
		t.Fatal(err)
	}
	paths := config.PathSet{WorkspaceDir: workspace, StateDir: state}
	p, err := broker.BuildSessionPolicy(profile, broker.SessionRoots{Paths: paths, Home: root, Temp: filepath.Join(root, "temp")})
	if err != nil {
		t.Fatal(err)
	}
	return broker.BuildPolicyEnforcedBroker(workspace, p, discardRecords(), memory.BuildStore(state), state, core.SystemClock{}), state
}

func TestRemoteMemoryRoundTripsWithoutExposingThePrivateStatePath(t *testing.T) {
	effects, state := buildMemoryBrokerForTest(t, "default")
	events := &recordingSink{}
	remote, _ := startPipedBrokerWith(t, effects, broker.RemoteOptions{Events: events})
	if err := remote.ReplaceMemory(context.Background(), "Call Alice."); err != nil {
		t.Fatal(err)
	}
	content, err := remote.ReadMemory(context.Background())
	if err != nil || !strings.Contains(content, "Call Alice.") {
		t.Fatalf("ReadMemory = %q, %v", content, err)
	}
	if _, err := remote.OpenFile(context.Background(), filepath.Join(state, memory.FileName)); !errors.Is(err, core.ErrDenied) {
		t.Fatalf("ordinary OpenFile on memory = %v, want denied", err)
	}
	if types := events.reportEventTypes(); len(types) != 1 || types[0] != core.EventPolicyDenied {
		t.Fatalf("events = %v, want PolicyDenied", types)
	}
}

func TestParanoidMemoryWriteIsDeniedWithAnEventAndNoFile(t *testing.T) {
	effects, state := buildMemoryBrokerForTest(t, "paranoid")
	events := &recordingSink{}
	remote, _ := startPipedBrokerWith(t, effects, broker.RemoteOptions{Events: events})
	if err := remote.ReplaceMemory(context.Background(), "do not write"); !errors.Is(err, core.ErrDenied) {
		t.Fatalf("ReplaceMemory = %v, want denied", err)
	}
	if _, err := os.Stat(filepath.Join(state, memory.FileName)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("memory file exists after denial: %v", err)
	}
	if types := events.reportEventTypes(); len(types) != 1 || types[0] != core.EventPolicyDenied {
		t.Fatalf("events = %v, want PolicyDenied", types)
	}
}

func TestUntrustedMemoryWriteRequiresARealApproval(t *testing.T) {
	effects, state := buildMemoryBrokerForTest(t, "default")
	approver := &scriptedApprover{decision: core.ApprovalDenied}
	events := &recordingSink{}
	remote, _ := startPipedBrokerWith(t, effects, broker.RemoteOptions{Approver: approver, Events: events})
	if err := remote.ReplaceMemory(core.MarkUntrustedStep(context.Background()), "from a web page"); !errors.Is(err, core.ErrDenied) {
		t.Fatalf("untrusted ReplaceMemory = %v, want denied", err)
	}
	if len(approver.asked) != 1 || approver.asked[0].Kind != core.CapabilityFSWrite {
		t.Fatalf("approval requests = %+v", approver.asked)
	}
	if _, err := os.Stat(filepath.Join(state, memory.FileName)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("memory file exists after denial: %v", err)
	}
	if types := events.reportEventTypes(); len(types) != 1 || types[0] != core.EventPolicyDenied {
		t.Fatalf("events = %v, want PolicyDenied", types)
	}
}
