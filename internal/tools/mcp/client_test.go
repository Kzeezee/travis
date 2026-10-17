package mcp

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/Kzeezee/travis/internal/config"
	"github.com/Kzeezee/travis/internal/core"
	"github.com/Kzeezee/travis/internal/tools/registry"
)

// TestBuildManagerConnectsAndRegistersAdaptedTools proves BuildManager does not block its
// caller: it returns immediately, and tools from a server that needs approval appear in the
// registry only once a (fake) human answers — never before, and never by blocking startup.
func TestBuildManagerConnectsAndRegistersAdaptedTools(t *testing.T) {
	approver := &fakeApprover{decision: core.ApprovalOnce}
	servers := config.MCPCfg{Servers: map[string]config.MCPServerCfg{"fake": buildTestServerConfig()}}
	reg := registry.BuildRegistry(100)

	returned := make(chan *Manager, 1)
	go func() {
		returned <- BuildManager(servers, grantedExecPolicy(), approver, core.FixedClock{}, nil, &syncBuffer{}, reg, t.TempDir(), testSandboxing())
	}()

	var manager *Manager
	select {
	case manager = <-returned:
	case <-time.After(2 * time.Second):
		t.Fatal("BuildManager did not return promptly; it must never block on a server's approval")
	}
	t.Cleanup(manager.Close)

	pollUntil(t, 10*time.Second, func() bool {
		_, ok := reg.Lookup("mcp_fake_echo")
		return ok
	})
	echoTool, ok := reg.Lookup("mcp_fake_echo")
	if !ok {
		t.Fatal("mcp_fake_echo was not registered")
	}
	if _, ok := reg.Lookup("mcp_fake_crash"); !ok {
		t.Fatal("mcp_fake_crash was not registered")
	}

	arguments, err := json.Marshal(map[string]string{"message": "hello from a test"})
	if err != nil {
		t.Fatal(err)
	}
	output, err := echoTool.Invoke(context.Background(), arguments)
	if err != nil {
		t.Fatalf("Invoke() error = %v", err)
	}
	if output.IsError {
		t.Fatalf("Invoke() = %+v, want IsError false", output)
	}
	if output.Display != "called mcp_fake_echo" {
		t.Fatalf("Invoke() Display = %q, want a clean human summary, never the raw untrusted marker (transcript.go falls back to Content's first line when Display is empty)", output.Display)
	}
	if !strings.Contains(output.Content, "hello from a test") {
		t.Fatalf("Invoke() content = %q, want it to contain the echoed message", output.Content)
	}
	if !strings.HasPrefix(output.Content, `<!-- BEGIN UNTRUSTED source="mcp:fake"`) {
		t.Fatalf("Invoke() content = %q, want it wrapped as untrusted and labelled with the server name", output.Content)
	}
}

// blockingApprover never answers until release fires — standing in for the real deadlock
// condition: a frontend whose event loop (the thing that would ever answer an approval)
// has not started yet.
type blockingApprover struct {
	release  <-chan struct{}
	decision core.ApprovalDecision
}

func (a *blockingApprover) RequestApproval(ctx context.Context, _ core.Operation) (core.ApprovalDecision, error) {
	select {
	case <-a.release:
		return a.decision, nil
	case <-ctx.Done():
		return core.ApprovalDenied, ctx.Err()
	}
}

// TestBuildManagerDoesNotBlockOnAPendingApproval is the regression test for the real bug
// this session hit: BuildManager used to connect synchronously, so a server needing Ask
// deadlocked the whole chat session before the TUI (which alone can answer an approval)
// ever started. A fakeApprover that answers instantly would not have caught this — it has
// to actually block, the way a real unstarted frontend does.
func TestBuildManagerDoesNotBlockOnAPendingApproval(t *testing.T) {
	release := make(chan struct{}) // never closed: this approval is never answered
	approver := &blockingApprover{release: release, decision: core.ApprovalOnce}
	servers := config.MCPCfg{Servers: map[string]config.MCPServerCfg{"fake": buildTestServerConfig()}}
	reg := registry.BuildRegistry(100)

	returned := make(chan *Manager, 1)
	go func() {
		returned <- BuildManager(servers, grantedExecPolicy(), approver, core.FixedClock{}, nil, &syncBuffer{}, reg, t.TempDir(), testSandboxing())
	}()

	select {
	case manager := <-returned:
		manager.Close()
	case <-time.After(2 * time.Second):
		t.Fatal("BuildManager blocked on a never-answered approval; this is the exact deadlock that made travis chat hang")
	}
}

// TestTrustPersistsAcrossManagersSharingAStateDir is the actual behavior this trust store
// exists to deliver: approve a server "always" once, and a brand new Manager — standing in
// for a brand new `travis chat` process — must connect without asking again, because the
// approval survived on disk, not just in that first Manager's memory.
func TestTrustPersistsAcrossManagersSharingAStateDir(t *testing.T) {
	stateDir := t.TempDir()
	servers := config.MCPCfg{Servers: map[string]config.MCPServerCfg{"fake": buildTestServerConfig()}}

	firstApprover := &fakeApprover{decision: core.ApprovalAlways}
	first := BuildManager(servers, grantedExecPolicy(), firstApprover, core.FixedClock{}, nil, &syncBuffer{}, registry.BuildRegistry(100), stateDir, testSandboxing())
	pollUntil(t, 10*time.Second, func() bool { return len(firstApprover.askedOps()) == 1 })
	first.Close()
	if asked := firstApprover.askedOps(); len(asked) != 1 {
		t.Fatalf("first manager asked %d times, want exactly 1", len(asked))
	}

	secondApprover := &fakeApprover{decision: core.ApprovalOnce} // must never be reached
	reg := registry.BuildRegistry(100)
	second := BuildManager(servers, grantedExecPolicy(), secondApprover, core.FixedClock{}, nil, &syncBuffer{}, reg, stateDir, testSandboxing())
	t.Cleanup(second.Close)

	pollUntil(t, 10*time.Second, func() bool {
		_, ok := reg.Lookup("mcp_fake_echo")
		return ok
	})
	if asked := secondApprover.askedOps(); len(asked) != 0 {
		t.Fatalf("second manager asked %d times, want 0: an already-trusted server must not re-ask across a fresh Manager sharing the same state dir", len(asked))
	}
}

func TestBuildManagerSkipsAServerDeniedByPolicy(t *testing.T) {
	servers := config.MCPCfg{Servers: map[string]config.MCPServerCfg{"fake": buildTestServerConfig()}}
	diagnostics := &syncBuffer{}
	reg := registry.BuildRegistry(100)
	manager := BuildManager(servers, deniedExecPolicy(), nil, core.FixedClock{}, nil, diagnostics, reg, t.TempDir(), testSandboxing())
	t.Cleanup(manager.Close)

	pollUntil(t, 2*time.Second, func() bool {
		return strings.Contains(diagnostics.String(), "fake")
	})
	if tools := reg.List(registry.Filter{}); len(tools) != 0 {
		t.Fatalf("registry = %v, want empty: policy denied this server outright", tools)
	}
}
