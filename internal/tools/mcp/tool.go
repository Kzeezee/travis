package mcp

import (
	"context"
	"encoding/json"
	"fmt"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/Kzeezee/travis/internal/core"
)

// adaptedTool exposes one tool from one connected MCP server as a core.Tool, namespaced so
// two servers can never collide on a tool name (matches tools/registry.go's toolNamePattern).
type adaptedTool struct {
	process    *serverProcess
	server     string
	remoteName string
	remoteTool *mcpsdk.Tool
}

func buildAdaptedTool(process *serverProcess, server string, remoteTool *mcpsdk.Tool) *adaptedTool {
	return &adaptedTool{process: process, server: server, remoteName: remoteTool.Name, remoteTool: remoteTool}
}

func (t *adaptedTool) Name() string {
	return toolNamePrefix(t.server) + t.remoteName
}

// toolNamePrefix is every tool name adaptedTool ever produces for server, so a server's
// tools can always be found again by name alone — used both by Name and, on restart
// exhaustion, to look the server's own tools back up in the registry to unregister them.
func toolNamePrefix(server string) string {
	return fmt.Sprintf("mcp_%s_", server)
}

func (t *adaptedTool) Describe() core.ToolSpec {
	schema, err := json.Marshal(t.remoteTool.InputSchema)
	if err != nil {
		schema = []byte(`{"type":"object"}`)
	}
	return core.ToolSpec{
		Name:        t.Name(),
		Description: t.remoteTool.Description,
		InputSchema: schema,
		// The exec capability this tool's existence depends on was already decided once,
		// at connect time (supervise.go); this just documents that dependency honestly.
		Capabilities: []core.Capability{{Kind: core.CapabilityExec, Scope: t.process.cfg.Command}},
	}
}

func (t *adaptedTool) Invoke(ctx context.Context, arguments json.RawMessage) (core.ToolOutput, error) {
	if err := ctx.Err(); err != nil {
		return core.ToolOutput{}, err
	}

	if core.ReportIsFromUntrustedStep(ctx) {
		if err := t.requestUntrustedCallApproval(ctx); err != nil {
			if ctxErr := ctx.Err(); ctxErr != nil {
				return core.ToolOutput{}, ctxErr
			}
			return core.ToolOutput{IsError: true, Content: `{"error":"mcp_call_denied"}`, Display: "mcp call denied"}, nil
		}
	}

	session, err := t.process.currentSession()
	if err != nil {
		return core.ToolOutput{IsError: true, Content: err.Error(), Display: "mcp server unavailable"}, nil
	}

	result, err := session.CallTool(ctx, &mcpsdk.CallToolParams{Name: t.remoteName, Arguments: arguments})
	if ctxErr := ctx.Err(); ctxErr != nil {
		return core.ToolOutput{}, ctxErr
	}
	if err != nil {
		return core.ToolOutput{IsError: true, Content: translateCallError(t.server, err).Error(), Display: "mcp call failed"}, nil
	}

	// Display is the human summary shown in the transcript card (transcript.go: "Content is
	// model-facing, so it is only a fallback"); it must never repeat server-origin text here
	// — only Content carries the untrusted wrap, Display stays a fixed, travis-authored label.
	display := "called " + t.Name()
	if result.IsError {
		display = "mcp tool reported an error"
	}
	text := extractText(result.Content)
	return core.ToolOutput{
		Content:     core.WrapUntrustedContent(sourceName(t.server), text),
		Display:     display,
		IsError:     result.IsError,
		IsUntrusted: true,
	}, nil
}

// requestUntrustedCallApproval asks before an MCP call in a step with untrusted intake: the
// server's effects never pass the broker, so this is the only place the step mark can bite.
func (t *adaptedTool) requestUntrustedCallApproval(ctx context.Context) error {
	if t.process.approver == nil {
		return core.ErrDenied
	}
	decision, err := t.process.approver.RequestApproval(ctx, core.Operation{
		Kind:           core.CapabilityExec,
		Target:         t.Name(),
		Explanation:    "call an MCP tool in a step with untrusted intake (§5.12.4)",
		IsIrreversible: true,
	})
	if err != nil {
		return err
	}
	if decision == core.ApprovalDenied {
		return core.ErrDenied
	}
	return nil
}

// extractText concatenates every text part of an MCP result; non-text content (images,
// resources) has no representation in core.ToolOutput's plain-string Content yet, so it is
// named but not rendered rather than silently dropped.
func extractText(content []mcpsdk.Content) string {
	var text string
	for _, part := range content {
		switch c := part.(type) {
		case *mcpsdk.TextContent:
			text += c.Text
		default:
			text += fmt.Sprintf("[non-text content: %T]", part)
		}
	}
	return text
}
