package core

import (
	"context"
	"time"
)

type EventType string

const (
	EventUserMessageSubmitted  EventType = "user_message.submitted"
	EventAssistantTextStreamed EventType = "assistant_text.streamed"
	EventToolCallStarted       EventType = "tool_call.started"
	EventToolCallFinished      EventType = "tool_call.finished"
	EventTurnCompleted         EventType = "turn.completed"
)

type Event interface {
	EventType() EventType
}

type EventSink interface {
	Publish(context.Context, Event)
}

type EventHeader struct {
	ID ID        `json:"id,omitempty"`
	At time.Time `json:"at"`
}

type UserMessageSubmitted struct {
	EventHeader
	Message Message `json:"message"`
}

func (UserMessageSubmitted) EventType() EventType {
	return EventUserMessageSubmitted
}

type AssistantTextStreamed struct {
	EventHeader
	Text string `json:"text"`
}

func (AssistantTextStreamed) EventType() EventType {
	return EventAssistantTextStreamed
}

type ToolCallStarted struct {
	EventHeader
	Call ToolCall `json:"call"`
}

func (ToolCallStarted) EventType() EventType {
	return EventToolCallStarted
}

type ToolCallFinished struct {
	EventHeader
	Result ToolResult `json:"result"`
}

func (ToolCallFinished) EventType() EventType {
	return EventToolCallFinished
}

type TurnCompleted struct {
	EventHeader
	Reason StopReason `json:"reason"`
	Usage  Usage      `json:"usage"`
}

func (TurnCompleted) EventType() EventType {
	return EventTurnCompleted
}
