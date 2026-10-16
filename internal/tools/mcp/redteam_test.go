package mcp

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/Kzeezee/travis/internal/core"
)

// TestRedTeamGateMCPServerCannotSeeTheProviderKey: a started server's environment carries
// no provider credential, and what it returns is marked untrusted for the loop (3F).
func TestRedTeamGateMCPServerCannotSeeTheProviderKey(t *testing.T) {
	const canary = "sk-canary-3f-do-not-leak"
	t.Setenv("OPENAI_API_KEY", canary)
	env := connectFakeTool(t, &fakeApprover{decision: core.ApprovalOnce}, "env")

	output, err := env.Invoke(t.Context(), json.RawMessage(`{"name":"OPENAI_API_KEY"}`))
	if err != nil || output.IsError || !strings.Contains(output.Content, "OPENAI_API_KEY=") {
		t.Fatalf("env tool = %+v, %v; want the server's view of OPENAI_API_KEY", output, err)
	}
	if strings.Contains(output.Content, canary) {
		t.Fatalf("env tool = %q, the server saw the provider key", output.Content)
	}
	if !output.IsUntrusted {
		t.Fatalf("output = %+v, want an MCP result marked untrusted", output)
	}
}

// A server approved "always" earlier still cannot start once exec_allow no longer covers it.
func TestRedTeamGateTrustedServerStillNeedsExecAllow(t *testing.T) {
	process, clock := buildTestProcess(t, buildTestServerConfig(), grantedExecPolicy(), &fakeApprover{decision: core.ApprovalDenied})
	if err := process.trust.add(process.cfg.Command, process.cfg.Args, "test", clock); err != nil {
		t.Fatal(err)
	}
	session, err := process.connect(t.Context())
	if err != nil {
		t.Fatalf("connect() error = %v, want the trusted server to start without asking", err)
	}
	_ = session.Close()

	process.policy = deniedExecPolicy()
	if _, err := process.connect(t.Context()); !errors.Is(err, core.ErrDenied) {
		t.Fatalf("connect() error = %v, want core.ErrDenied despite the trust entry", err)
	}
}

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
