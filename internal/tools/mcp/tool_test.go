package mcp

import (
	"strings"
	"testing"

	"github.com/Kzeezee/travis/internal/core"
	"github.com/Kzeezee/travis/internal/tools/tooltest"
)

// TestAdaptedToolMeetsTheToolContract proves an MCP-adapted tool is a well-behaved
// core.Tool, the same bar every builtin tool is held to (tooltest.Run's own doc comment).
func TestAdaptedToolMeetsTheToolContract(t *testing.T) {
	approver := &fakeApprover{decision: core.ApprovalOnce}
	process, _ := buildTestProcess(t, buildTestServerConfig(), grantedExecPolicy(), approver)
	t.Cleanup(func() {
		if session, err := process.currentSession(); err == nil && session != nil {
			session.Close()
		}
	})

	ctx := t.Context()
	session, err := process.connect(ctx)
	if err != nil {
		t.Fatalf("connect() error = %v", err)
	}
	process.session = session

	listed, err := session.ListTools(ctx, nil)
	if err != nil {
		t.Fatalf("ListTools() error = %v", err)
	}
	var echo *adaptedTool
	for _, remoteTool := range listed.Tools {
		if remoteTool.Name == "echo" {
			echo = buildAdaptedTool(process, "fake", remoteTool)
		}
	}
	if echo == nil {
		t.Fatal("fakeserver did not list an echo tool")
	}

	tooltest.Run(t, echo)

	if got := echo.Name(); !strings.HasPrefix(got, "mcp_fake_") {
		t.Fatalf("Name() = %q, want it namespaced mcp_<server>_<tool>", got)
	}
}
