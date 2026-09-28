// Flow: Tool.Describe → ToolSpec (what is this tool) → ToolCall (model asks for tool) →
// Tool.Invoke → ToolOutput (tool answers) → ToolResult (loop replies to model)

package core

import (
	"context"
	"encoding/json"
)

type Tool interface {
	Name() string
	Describe() ToolSpec
	// error means the turn is in trouble; a request the tool could not fulfil is ToolOutput.IsError
	Invoke(ctx context.Context, arguments json.RawMessage) (ToolOutput, error)
}

type ToolSpec struct {
	Name         string          `json:"name"`
	Description  string          `json:"description,omitempty"`
	InputSchema  json.RawMessage `json:"input_schema,omitempty"`
	Capabilities []Capability    `json:"capabilities,omitempty"`
}

type ToolCall struct {
	ID        ID              `json:"id"`
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

type ToolOutput struct {
	Content string
	Display string
	IsError bool
}

type ToolResult struct {
	CallID  ID     `json:"call_id"`
	Name    string `json:"name"`
	Content string `json:"content,omitempty"`
	IsError bool   `json:"is_error,omitempty"`
}
