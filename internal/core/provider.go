package core

import "context"

type Provider interface {
	Chat(context.Context, ProviderRequest) (Stream, error)
}

type ProviderRequest struct {
	Model     string     `json:"model"`
	Messages  []Message  `json:"messages"`
	Tools     []ToolSpec `json:"tools,omitempty"`
	MaxTokens int        `json:"max_tokens,omitempty"`
}

type Stream interface {
	Next() (Chunk, error)
	Close() error
}

type Chunk struct {
	Text      string            `json:"text,omitempty"`
	Reasoning string            `json:"reasoning,omitempty"`
	ToolCall  *ToolCallFragment `json:"tool_call,omitempty"`
	Usage     *Usage            `json:"usage,omitempty"`
	Finish    StopReason        `json:"finish,omitempty"`
}

type ToolCallFragment struct {
	ID        ID     `json:"id,omitempty"`
	Name      string `json:"name,omitempty"`
	Arguments string `json:"arguments,omitempty"`
}

type Usage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}

type StopReason string

const (
	StopEndTurn   StopReason = "end_turn"
	StopToolCalls StopReason = "tool_calls"
	StopLength    StopReason = "length"
	StopCanceled  StopReason = "canceled"
	StopError     StopReason = "error"
	StopLooping   StopReason = "looping"
	StopBudget    StopReason = "budget"
)
