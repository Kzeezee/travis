package core

import "context"

type Provider interface {
	Chat(context.Context, ProviderRequest) (Stream, error)
}

type ProviderRequest struct {
	Model string `json:"model"`
	// System is the standing instruction, kept out of Messages so each adapter can place
	// it where its API expects (a leading message for OpenAI, a top-level field elsewhere).
	System    string     `json:"system,omitempty"`
	Messages  []Message  `json:"messages"`
	Tools     []ToolSpec `json:"tools,omitempty"`
	MaxTokens int        `json:"max_tokens,omitempty"`
}

type Stream interface {
	Next() (Chunk, error)
	Close() error
}

type Chunk struct {
	Text      string     `json:"text,omitempty"`
	Reasoning string     `json:"reasoning,omitempty"`
	ToolCalls []ToolCall `json:"tool_calls,omitempty"` // assembled, on the chunk carrying Finish
	Usage     *Usage     `json:"usage,omitempty"`
	Finish    StopReason `json:"finish,omitempty"`
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
