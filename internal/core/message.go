package core

import "encoding/json"

// Roles identify who produced a conversation message.
type Role string

const (
	RoleSystem    Role = "system"
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
	RoleTool      Role = "tool"
)

// Part kinds identify the shape of one piece of message content.
type PartKind string

const (
	PartText       PartKind = "text"
	PartToolCall   PartKind = "tool_call"
	PartToolResult PartKind = "tool_result"
)

// Message is provider-neutral conversation content passed between app modules.
type Message struct {
	Role  Role   `json:"role"`
	Parts []Part `json:"parts"`
}

// Part is one strictly typed piece of message content.
type Part interface {
	ReportPartKind() PartKind
}

// TextPart carries normal text content.
type TextPart struct {
	Text string `json:"text"`
}

func (TextPart) ReportPartKind() PartKind {
	return PartText
}

func (p TextPart) MarshalJSON() ([]byte, error) {
	type textPart struct {
		Kind PartKind `json:"kind"`
		Text string   `json:"text"`
	}
	return json.Marshal(textPart{Kind: p.ReportPartKind(), Text: p.Text})
}

// ToolCallPart carries a tool call requested by the assistant.
type ToolCallPart struct {
	ToolCall ToolCall `json:"tool_call"`
}

func (ToolCallPart) ReportPartKind() PartKind {
	return PartToolCall
}

func (p ToolCallPart) MarshalJSON() ([]byte, error) {
	type toolCallPart struct {
		Kind     PartKind `json:"kind"`
		ToolCall ToolCall `json:"tool_call"`
	}
	return json.Marshal(toolCallPart{Kind: p.ReportPartKind(), ToolCall: p.ToolCall})
}

// ToolResultPart carries the output from a completed tool call.
type ToolResultPart struct {
	ToolResult ToolResult `json:"tool_result"`
}

func (ToolResultPart) ReportPartKind() PartKind {
	return PartToolResult
}

func (p ToolResultPart) MarshalJSON() ([]byte, error) {
	type toolResultPart struct {
		Kind       PartKind   `json:"kind"`
		ToolResult ToolResult `json:"tool_result"`
	}
	return json.Marshal(toolResultPart{Kind: p.ReportPartKind(), ToolResult: p.ToolResult})
}
