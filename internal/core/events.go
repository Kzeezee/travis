package core

import (
	"context"
	"time"
)

// Event types identify facts that already happened in the app timeline.
type EventType string

const (
	EventUserMessageSubmitted  EventType = "user_message.submitted"
	EventAssistantTextStreamed EventType = "assistant_text.streamed"
	EventToolCallStarted       EventType = "tool_call.started"
	EventToolCallFinished      EventType = "tool_call.finished"
	EventTurnCompleted         EventType = "turn.completed"
)

// Event is the shared interface for timeline facts observed by UI, traces, or tests.
type Event interface {
	ReportEventType() EventType
}

// EventSink receives observed events without making delivery required for correctness.
type EventSink interface {
	Publish(context.Context, Event)
}

// EventHeader carries metadata common to every event.
type EventHeader struct {
	ID ID        `json:"id,omitempty"`
	At time.Time `json:"at"`
}

// UserMessageSubmitted records that a user message entered the app.
type UserMessageSubmitted struct {
	EventHeader
	Message Message `json:"message"`
}

func (UserMessageSubmitted) ReportEventType() EventType {
	return EventUserMessageSubmitted
}

// AssistantTextStreamed records one streamed assistant text chunk.
type AssistantTextStreamed struct {
	EventHeader
	Text string `json:"text"`
}

func (AssistantTextStreamed) ReportEventType() EventType {
	return EventAssistantTextStreamed
}

// ToolCallStarted records that a tool call began.
type ToolCallStarted struct {
	EventHeader
	Call ToolCall `json:"call"`
}

func (ToolCallStarted) ReportEventType() EventType {
	return EventToolCallStarted
}

// ToolCallFinished records that a tool call produced a result.
type ToolCallFinished struct {
	EventHeader
	Result  ToolResult `json:"result"`
	Display string     `json:"display,omitempty"`
}

func (ToolCallFinished) ReportEventType() EventType {
	return EventToolCallFinished
}

// TurnCompleted records why an assistant turn stopped.
type TurnCompleted struct {
	EventHeader
	Reason StopReason `json:"reason"`
	Usage  Usage      `json:"usage"`
}

func (TurnCompleted) ReportEventType() EventType {
	return EventTurnCompleted
}
