package core

import "encoding/json"

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

type ToolResult struct {
	CallID  ID     `json:"call_id"`
	Name    string `json:"name"`
	Content string `json:"content,omitempty"`
	IsError bool   `json:"is_error,omitempty"`
}
