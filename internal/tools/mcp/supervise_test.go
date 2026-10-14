package mcp

import (
	"context"
	"errors"
	"testing"
	"time"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/Kzeezee/travis/internal/core"
)

// pollUntil polls fn every 20ms until it returns true or deadline elapses, failing the test
// otherwise; used for the restart-supervision test, which reconnects on a background goroutine.
func pollUntil(t *testing.T, deadline time.Duration, fn func() bool) {
	t.Helper()
	cutoff := time.Now().Add(deadline)
	for time.Now().Before(cutoff) {
		if fn() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("condition did not become true within %s", deadline)
}

func TestConnectDeniesWithNoExecGrantWithoutAsking(t *testing.T) {
	approver := &fakeApprover{decision: core.ApprovalOnce}
	process, _ := buildTestProcess(t, buildTestServerConfig(), deniedExecPolicy(), approver)

	if _, err := process.connect(context.Background()); !errors.Is(err, core.ErrDenied) {
		t.Fatalf("connect() error = %v, want ErrDenied", err)
	}
	if len(approver.asked) != 0 {
		t.Fatalf("approver.asked = %+v, want none: a request with no grant at all denies outright, it never asks", approver.asked)
	}
}

func TestConnectAsksThenConnectsOnApproval(t *testing.T) {
	approver := &fakeApprover{decision: core.ApprovalOnce}
	process, _ := buildTestProcess(t, buildTestServerConfig(), grantedExecPolicy(), approver)

	ctx := context.Background()
	session, err := process.connect(ctx)
	if err != nil {
		t.Fatalf("connect() error = %v", err)
	}
	defer session.Close()

	if len(approver.asked) != 1 {
		t.Fatalf("approver.asked = %+v, want exactly one request", approver.asked)
	}
	if approver.asked[0].IsIrreversible {
		t.Fatal("the exec Operation should not be IsIrreversible: unlike generic exec, this approval is scoped to the exact (command, args) the trust store ties to, so Always must be offerable")
	}

	listed, err := session.ListTools(ctx, &mcpsdk.ListToolsParams{})
	if err != nil {
		t.Fatalf("ListTools() error = %v", err)
	}
	if len(listed.Tools) != 2 {
		t.Fatalf("ListTools() returned %d tools, want 2 (echo, crash)", len(listed.Tools))
	}
}

func TestConnectDeniedByApprover(t *testing.T) {
	approver := &fakeApprover{decision: core.ApprovalDenied}
	process, _ := buildTestProcess(t, buildTestServerConfig(), grantedExecPolicy(), approver)

	if _, err := process.connect(context.Background()); !errors.Is(err, core.ErrDenied) {
		t.Fatalf("connect() error = %v, want ErrDenied", err)
	}
}

// TestServerProcessRestartsAfterCrash proves restart-with-backoff against a real dead
// subprocess: the crash tool exits the fake server, and supervision should reconnect
// without any new approval (the same server was already approved once this session).
func TestServerProcessRestartsAfterCrash(t *testing.T) {
	approver := &fakeApprover{decision: core.ApprovalOnce}
	process, _ := buildTestProcess(t, buildTestServerConfig(), grantedExecPolicy(), approver)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	session, err := process.connectAndSupervise(ctx)
	if err != nil {
		t.Fatalf("connectAndSupervise() error = %v", err)
	}

	// Triggering the crash tool races the response against the process exiting; either a
	// transport error or a clean reply is an acceptable outcome here, only the eventual
	// restart matters.
	_, _ = session.CallTool(ctx, &mcpsdk.CallToolParams{Name: "crash"})

	pollUntil(t, 10*time.Second, func() bool {
		current, err := process.currentSession()
		if err != nil || current == nil {
			return false
		}
		result, err := current.CallTool(ctx, &mcpsdk.CallToolParams{Name: "echo", Arguments: map[string]any{"message": "back up"}})
		return err == nil && !result.IsError
	})
	// ApprovalOnce authorizes exactly one launch, not "this server for the rest of the
	// session" — so a crash-triggered restart asks again. Only ApprovalAlways would skip
	// re-asking, and policy.go's D-27 stance (exec is irreversible) means the approval
	// modal never actually offers Always for this operation in the first place.
	if len(approver.asked) != 2 {
		t.Fatalf("approver.asked = %+v, want exactly two requests: the first connect, then the crash-triggered restart", approver.asked)
	}
}
