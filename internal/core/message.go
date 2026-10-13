package core

import (
	"bytes"
	"encoding/json"
	"fmt"
)

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

// UnmarshalJSON is the inverse of the parts' MarshalJSON: each part is decoded by its kind, and
// an unknown kind or field is refused, since stored messages are read back across a trust boundary.
func (m *Message) UnmarshalJSON(data []byte) error {
	var raw struct {
		Role  Role              `json:"role"`
		Parts []json.RawMessage `json:"parts"`
	}
	if err := decodeStrictJSON(data, &raw); err != nil {
		return err
	}
	parts := make([]Part, 0, len(raw.Parts))
	for _, rawPart := range raw.Parts {
		part, err := decodeMessagePart(rawPart)
		if err != nil {
			return err
		}
		parts = append(parts, part)
	}
	m.Role, m.Parts = raw.Role, parts
	return nil
}

func decodeMessagePart(data json.RawMessage) (Part, error) {
	var kind struct {
		Kind MessageContentType `json:"kind"`
	}
	if err := json.Unmarshal(data, &kind); err != nil {
		return nil, err
	}
	switch kind.Kind {
	case MessageContentText:
		var part struct {
			Kind MessageContentType `json:"kind"`
			Text string             `json:"text"`
		}
		err := decodeStrictJSON(data, &part)
		return TextPart{Text: part.Text}, err
	case MessageContentToolCall:
		var part struct {
			Kind     MessageContentType `json:"kind"`
			ToolCall ToolCall           `json:"tool_call"`
		}
		err := decodeStrictJSON(data, &part)
		return ToolCallPart{ToolCall: part.ToolCall}, err
	case MessageContentToolResult:
		var part struct {
			Kind       MessageContentType `json:"kind"`
			ToolResult ToolResult         `json:"tool_result"`
		}
		err := decodeStrictJSON(data, &part)
		return ToolResultPart{ToolResult: part.ToolResult}, err
	}
	return nil, fmt.Errorf("unknown message part kind %q: %w", kind.Kind, ErrInvalid)
}

func decodeStrictJSON(data []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return fmt.Errorf("%w: %w", ErrInvalid, err)
	}
	return nil
}
