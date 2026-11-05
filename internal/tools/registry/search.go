package registry

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/Kzeezee/travis/internal/core"
)

const (
	searchToolName   = "tool_search"
	searchToolSchema = `{"type":"object","properties":{"query":{"type":"string","description":"Words describing the capability needed"}},"required":["query"],"additionalProperties":false}`
	maxLoadedPerCall = 5
)

// searchTool lets the model load MCP tools on demand when too many are registered to list up front.
type searchTool struct {
	registry *Registry
}

func (t *searchTool) Name() string { return searchToolName }

func (t *searchTool) Describe() core.ToolSpec {
	return core.ToolSpec{
		Name:        searchToolName,
		Description: "Find and load MCP tools by keyword. Loaded tools become callable from your next step.",
		InputSchema: json.RawMessage(searchToolSchema),
	}
}

// Invoke reports tool names only, never MCP descriptions, so no untrusted text enters the result.
func (t *searchTool) Invoke(ctx context.Context, arguments json.RawMessage) (core.ToolOutput, error) {
	if err := ctx.Err(); err != nil {
		return core.ToolOutput{}, err
	}

	var args struct {
		Query string `json:"query"`
	}
	if err := json.Unmarshal(arguments, &args); err != nil || strings.TrimSpace(args.Query) == "" {
		return core.ToolOutput{IsError: true, Content: `{"error":"invalid_arguments"}`, Display: "tool_search: invalid arguments"}, nil
	}

	mcpTools := t.registry.List(Filter{Source: SourceMCP})
	matches := rankToolsByQueryOverlap(mcpTools, args.Query)
	matchedNames := make([]string, 0, maxLoadedPerCall)
	for _, tool := range matches[:min(len(matches), maxLoadedPerCall)] {
		matchedNames = append(matchedNames, tool.Name())
	}

	result := searchResult{Loaded: t.registry.loadMCPTools(matchedNames)}
	if len(matchedNames) == 0 {
		result.Hint = "no tool matched; try other words"
	}
	content, err := json.Marshal(result)
	if err != nil {
		return core.ToolOutput{}, err
	}
	return core.ToolOutput{Content: string(content), Display: "tool_search: " + args.Query}, nil
}

type searchResult struct {
	Loaded []string `json:"loaded"`
	Hint   string   `json:"hint,omitempty"`
}
