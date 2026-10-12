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

type toolCallIDKey struct{}

// WithToolCallID marks ctx as serving one tool call, so effects deep below the tool (a broker
// refusal) can name the call that caused them without every signature carrying it.
func WithToolCallID(ctx context.Context, id ID) context.Context {
	return context.WithValue(ctx, toolCallIDKey{}, id)
}

// ReportToolCallID is the call ctx serves, or "" outside a tool call.
func ReportToolCallID(ctx context.Context) ID {
	id, _ := ctx.Value(toolCallIDKey{}).(ID)
	return id
}

type untrustedStepKey struct{}

// MarkUntrustedStep marks ctx as serving a step with untrusted intake (§5.12.4), so policy asks
// a human before that step's effects instead of allowing them outright.
func MarkUntrustedStep(ctx context.Context) context.Context {
	return context.WithValue(ctx, untrustedStepKey{}, true)
}

// ReportIsFromUntrustedStep reports whether ctx serves a step MarkUntrustedStep marked.
func ReportIsFromUntrustedStep(ctx context.Context) bool {
	isUntrusted, _ := ctx.Value(untrustedStepKey{}).(bool)
	return isUntrusted
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
