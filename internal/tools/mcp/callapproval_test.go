package mcp

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/Kzeezee/travis/internal/config"
	"github.com/Kzeezee/travis/internal/core"
	"github.com/Kzeezee/travis/internal/tools/registry"
)

// connectAskingTool starts the fake server under approval_default = "ask", approving its launch, and adapts name.
func connectAskingTool(t *testing.T, approver *fakeApprover, name string) *adaptedTool {
	t.Helper()
	tool := connectFakeTool(t, approver, name)
	tool.process.callApproval = CallApprovalAsk
	approver.mu.Lock()
	approver.asked = nil
	approver.mu.Unlock()
	return tool
}

func answerWith(approver *fakeApprover, decision core.ApprovalDecision) {
	approver.mu.Lock()
	defer approver.mu.Unlock()
	approver.decision = decision
}

func invokeEcho(t *testing.T, tool *adaptedTool, untrusted bool) core.ToolOutput {
	t.Helper()
	ctx := t.Context()
	if untrusted {
		ctx = core.MarkUntrustedStep(ctx)
	}
	output, err := tool.Invoke(ctx, json.RawMessage(`{"message":"ran"}`))
	if err != nil {
		t.Fatal(err)
	}
	return output
}

func TestAskModeAsksBeforeEveryCallUntilAlways(t *testing.T) {
	approver := &fakeApprover{decision: core.ApprovalOnce}
	echo := connectAskingTool(t, approver, "echo")

	answerWith(approver, core.ApprovalDenied)
	if output := invokeEcho(t, echo, false); !output.IsError || strings.Contains(output.Content, "ran") {
		t.Fatalf("denied call = %+v; want a refusal that never reached the server", output)
	}
	answerWith(approver, core.ApprovalOnce)
	if output := invokeEcho(t, echo, false); output.IsError {
		t.Fatalf("approved call = %+v", output)
	}
	asked := approver.askedOps()
	if len(asked) != 2 || asked[0].Target != echo.Name() || asked[0].IsIrreversible {
		t.Fatalf("asked %+v; want two reversible-mode asks naming %s, so always can be offered", asked, echo.Name())
	}

	answerWith(approver, core.ApprovalAlways)
	invokeEcho(t, echo, false)
	answerWith(approver, core.ApprovalDenied)
	if output := invokeEcho(t, echo, false); output.IsError {
		t.Fatalf("call after always = %+v; want it to run without asking", output)
	}
	if got := len(approver.askedOps()); got != 3 {
		t.Fatalf("asked %d times; want always to stop the asking", got)
	}
}

func TestAlwaysForOneMCPToolIsRememberedForThatToolAndLaunchOnly(t *testing.T) {
	approver := &fakeApprover{decision: core.ApprovalAlways}
	echo := connectAskingTool(t, approver, "echo")
	invokeEcho(t, echo, false)

	command, args, grant := echo.reportApprovedLaunch()
	remembered := buildTrustStore(echo.process.trust.path)
	switch {
	case !remembered.isTrusted(command, args, grant, echo.Name()):
		t.Fatal("always was not remembered for the next session")
	case remembered.isTrusted(command, args, grant, "mcp_fake_other"):
		t.Fatal("always for one tool trusted another")
	case remembered.isTrusted(command, append(args, "--other-version"), grant, echo.Name()):
		t.Fatal("always outlived a change to the server's launch")
	}
}

// TestRedTeamGateRememberedMCPToolTrustStillAsksAfterUntrustedIntake: "always" relaxes the ask mode, never the
// escalation after untrusted content, which stays irreversible so it can never be learned (§5.12.4, §6.4).
func TestRedTeamGateRememberedMCPToolTrustStillAsksAfterUntrustedIntake(t *testing.T) {
	approver := &fakeApprover{decision: core.ApprovalAlways}
	echo := connectAskingTool(t, approver, "echo")
	invokeEcho(t, echo, false)

	answerWith(approver, core.ApprovalDenied)
	if output := invokeEcho(t, echo, true); !output.IsError {
		t.Fatalf("untrusted-step call = %+v; want it denied despite always", output)
	}
	asked := approver.askedOps()
	if last := asked[len(asked)-1]; len(asked) != 2 || !last.IsIrreversible {
		t.Fatalf("asked %+v; want the untrusted-step call asked as irreversible", asked)
	}
}

func TestNeverModeStartsNoServerAndAsksNothing(t *testing.T) {
	servers := config.MCPCfg{Servers: map[string]config.MCPServerCfg{"fake": buildTestServerConfig()}}
	reg := registry.BuildRegistry(100)
	approver := &fakeApprover{decision: core.ApprovalOnce}
	manager := BuildManager(servers, grantedExecPolicy(), approver, core.FixedClock{}, nil, &syncBuffer{}, reg, t.TempDir(), testSandboxing(), CallApprovalNever)
	manager.WaitUntilStarted()
	manager.Close()

	status := manager.ReportServerStatuses()[0]
	if status.State != ServerOff || len(reg.List(registry.Filter{Source: registry.SourceMCP})) != 0 || len(approver.askedOps()) != 0 {
		t.Fatalf("status %+v, asked %v; want the server off, no tools and no launch approval", status, approver.askedOps())
	}
}
