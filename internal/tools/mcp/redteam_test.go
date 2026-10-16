package mcp

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/Kzeezee/travis/internal/core"
)

// TestRedTeamGateMCPCallInAnUntrustedStepAsks: an injected result cannot drive another MCP
// call (whose effects never pass the broker) without a human saying yes.
func TestRedTeamGateMCPCallInAnUntrustedStepAsks(t *testing.T) {
	approver := &fakeApprover{decision: core.ApprovalOnce}
	echo := connectFakeTool(t, approver, "echo")
	approver.mu.Lock()
	approver.decision, approver.asked = core.ApprovalDenied, nil
	approver.mu.Unlock()

	untrusted := core.MarkUntrustedStep(t.Context())
	output, err := echo.Invoke(untrusted, json.RawMessage(`{"message":"ran"}`))
	if err != nil || !output.IsError || strings.Contains(output.Content, "ran") {
		t.Fatalf("denied call = %+v, %v; want a refusal that never reached the server", output, err)
	}
	asked := approver.askedOps()
	if len(asked) != 1 || asked[0].Target != echo.Name() || !asked[0].IsIrreversible {
		t.Fatalf("asked %+v, want one irreversible approval naming %s", asked, echo.Name())
	}

	output, err = echo.Invoke(t.Context(), json.RawMessage(`{"message":"ran"}`))
	if err != nil || output.IsError || len(approver.askedOps()) != 1 {
		t.Fatalf("trusted-step call = %+v, %v; want it to run without asking", output, err)
	}
}

// connectFakeTool starts the fake server, approving its launch, and adapts its tool name.
func connectFakeTool(t *testing.T, approver *fakeApprover, name string) *adaptedTool {
	t.Helper()
	process, _ := buildTestProcess(t, buildTestServerConfig(), grantedExecPolicy(), approver)
	session, err := process.connect(t.Context())
	if err != nil {
		t.Fatalf("connect() error = %v", err)
	}
	t.Cleanup(func() { _ = session.Close() })
	process.session = session
	listed, err := session.ListTools(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, remoteTool := range listed.Tools {
		if remoteTool.Name == name {
			return buildAdaptedTool(process, "fake", remoteTool)
		}
	}
	t.Fatalf("fakeserver did not list a %s tool", name)
	return nil
}
