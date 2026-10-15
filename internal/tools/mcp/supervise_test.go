package mcp

import (
	"context"
	"errors"
	"sync"
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
	if len(approver.askedOps()) != 0 {
		t.Fatalf("approver.asked = %+v, want none: a request with no grant at all denies outright, it never asks", approver.askedOps())
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

	if len(approver.askedOps()) != 1 {
		t.Fatalf("approver.asked = %+v, want exactly one request", approver.askedOps())
	}
	if approver.askedOps()[0].IsIrreversible {
		t.Fatal("the exec Operation should not be IsIrreversible: unlike generic exec, this approval is scoped to the exact (command, args) the trust store ties to, so Always must be offerable")
	}

	listed, err := session.ListTools(ctx, &mcpsdk.ListToolsParams{})
	if err != nil {
		t.Fatalf("ListTools() error = %v", err)
	}
	if len(listed.Tools) != 3 {
		t.Fatalf("ListTools() returned %d tools, want 3 (echo, crash, env)", len(listed.Tools))
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
	if len(approver.askedOps()) != 2 {
		t.Fatalf("approver.asked = %+v, want exactly two requests: the first connect, then the crash-triggered restart", approver.askedOps())
	}
}

// TestServerProcessUnregistersItsToolsOnceRestartsAreExhausted proves the registry side of
// 3C: once a server crashes more times than superviseRestarts will tolerate, it gives up for
// good (reportUnavailable) and the unregister callback fires exactly once.
func TestServerProcessUnregistersItsToolsOnceRestartsAreExhausted(t *testing.T) {
	approver := &fakeApprover{decision: core.ApprovalOnce}
	var mu sync.Mutex
	unregisteredCalls := 0
	process, _ := buildTestProcessWithUnregister(t, buildTestServerConfig(), grantedExecPolicy(), approver, func() {
		mu.Lock()
		defer mu.Unlock()
		unregisteredCalls++
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	session, err := process.connectAndSupervise(ctx)
	if err != nil {
		t.Fatalf("connectAndSupervise() error = %v", err)
	}

	// Each crash before the last must be survived by a successful restart, or the next
	// crash call races a process that is already gone. The final crash pushes the attempt
	// counter past maxRestartAttempts, so superviseRestarts gives up instead of restarting.
	for attempt := 1; attempt <= maxRestartAttempts+1; attempt++ {
		_, _ = session.CallTool(ctx, &mcpsdk.CallToolParams{Name: "crash"})
		if attempt > maxRestartAttempts {
			break
		}
		pollUntil(t, 10*time.Second, func() bool {
			current, err := process.currentSession()
			if err != nil || current == nil {
				return false
			}
			result, err := current.CallTool(ctx, &mcpsdk.CallToolParams{Name: "echo", Arguments: map[string]any{"message": "back up"}})
			if err != nil || result.IsError {
				return false
			}
			session = current
			return true
		})
	}

	pollUntil(t, 10*time.Second, func() bool {
		_, err := process.currentSession()
		return err != nil
	})

	mu.Lock()
	defer mu.Unlock()
	if unregisteredCalls != 1 {
		t.Fatalf("unregisterTools called %d times, want exactly 1", unregisteredCalls)
	}
}
