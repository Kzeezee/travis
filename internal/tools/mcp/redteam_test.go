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
	process, _ := buildTestProcess(t, buildTestServerConfig(), grantedExecPolicy(), &fakeApprover{decision: core.ApprovalOnce})
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
	var env *adaptedTool
	for _, remoteTool := range listed.Tools {
		if remoteTool.Name == "env" {
			env = buildAdaptedTool(process, "fake", remoteTool)
		}
	}
	if env == nil {
		t.Fatal("fakeserver did not list an env tool")
	}

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
func TestConnectDeniesATrustedServerThatPolicyNoLongerGrants(t *testing.T) {
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
