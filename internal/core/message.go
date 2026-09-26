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

// MessageContentType identifies which concrete content payload a message part carries.
// The current payloads are normal text, an assistant-requested tool call, and a tool result.
type MessageContentType string

const (
	MessageContentText       MessageContentType = "text"
	MessageContentToolCall   MessageContentType = "tool_call"
	MessageContentToolResult MessageContentType = "tool_result"
)

// Message is provider-neutral conversation content passed between app modules.
type Message struct {
	Role  Role   `json:"role"`
	Parts []Part `json:"parts"`
}

// Part is one strictly typed piece of message content.
type Part interface {
	ReportMessageContentType() MessageContentType
}

// TextPart carries normal text content.
type TextPart struct {
	Text string `json:"text"`
}

func (TextPart) ReportMessageContentType() MessageContentType {
	return MessageContentText
}

func (p TextPart) MarshalJSON() ([]byte, error) {
	type textPart struct {
		Kind MessageContentType `json:"kind"`
		Text string             `json:"text"`
	}
	return json.Marshal(textPart{Kind: p.ReportMessageContentType(), Text: p.Text})
}

// ToolCallPart carries a tool call requested by the assistant.
type ToolCallPart struct {
	ToolCall ToolCall `json:"tool_call"`
}

func (ToolCallPart) ReportMessageContentType() MessageContentType {
	return MessageContentToolCall
}

func (p ToolCallPart) MarshalJSON() ([]byte, error) {
	type toolCallPart struct {
		Kind     MessageContentType `json:"kind"`
		ToolCall ToolCall           `json:"tool_call"`
	}
	return json.Marshal(toolCallPart{Kind: p.ReportMessageContentType(), ToolCall: p.ToolCall})
}

// ToolResultPart carries the output from a completed tool call.
type ToolResultPart struct {
	ToolResult ToolResult `json:"tool_result"`
}

func (ToolResultPart) ReportMessageContentType() MessageContentType {
	return MessageContentToolResult
}

func (p ToolResultPart) MarshalJSON() ([]byte, error) {
	type toolResultPart struct {
		Kind       MessageContentType `json:"kind"`
		ToolResult ToolResult         `json:"tool_result"`
	}
	return json.Marshal(toolResultPart{Kind: p.ReportMessageContentType(), ToolResult: p.ToolResult})
}
