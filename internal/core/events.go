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
	EventThoughtStreamed       EventType = "thought.streamed"
	EventToolCallStarted       EventType = "tool_call.started"
	EventToolCallFinished      EventType = "tool_call.finished"
	EventTurnCompleted         EventType = "turn.completed"
	EventPolicyDenied          EventType = "policy.denied"
	EventBrokerUnavailable     EventType = "broker.unavailable"
	EventMCPServerUnavailable  EventType = "mcp_server.unavailable"
	EventMCPServerStarted      EventType = "mcp_server.started"
	EventFileChanged           EventType = "file.changed"
	EventFilesReverted         EventType = "files.reverted"
	EventConversationRewound   EventType = "conversation.rewound"
	EventSpeechRequested       EventType = "speech.requested"
)

// Event is the shared interface for timeline facts observed by UI, traces, or tests.
type Event interface {
	ReportEventType() EventType
}

// EventSink receives observed events without making delivery required for correctness.
type EventSink interface {
	Publish(context.Context, Event)
}

// EventHeader carries metadata common to every event. TurnID groups the facts of one
// assistant turn so a trace can be read back turn by turn (DESIGN.md §5.13).
type EventHeader struct {
	ID     ID        `json:"id,omitempty"`
	TurnID ID        `json:"turn_id,omitempty"`
	At     time.Time `json:"at"`
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

// ThoughtStreamed records one streamed reasoning chunk; it is never part of the answer.
type ThoughtStreamed struct {
	EventHeader
	Text string `json:"text"`
}

func (ThoughtStreamed) ReportEventType() EventType {
	return EventThoughtStreamed
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

// PolicyDenied records that the broker refused an effect, naming the rule that decided it
// (D-29: a red-team case asserts this event fired, not merely that the effect is absent).
// CallID is the tool call that asked, empty for an effect outside one.
type PolicyDenied struct {
	EventHeader
	CallID     ID         `json:"call_id,omitempty"`
	Capability Capability `json:"capability"`
	Rule       string     `json:"rule"`
}

func (PolicyDenied) ReportEventType() EventType {
	return EventPolicyDenied
}

// BrokerUnavailable records that the broker connection broke. Every later effect fails
// closed, so the frontend shows it until the session ends; there is no degraded mode.
type BrokerUnavailable struct {
	EventHeader
	Reason string `json:"reason"`
}

func (BrokerUnavailable) ReportEventType() EventType {
	return EventBrokerUnavailable
}

// MCPServerUnavailable records that a configured local MCP server exhausted its restart
// attempts. Its tools are no longer in the registry; there is no degraded mode where a
// dead server's tools are offered but fail every call.
type MCPServerUnavailable struct {
	EventHeader
	Server string `json:"server"`
	Reason string `json:"reason"`
}

func (MCPServerUnavailable) ReportEventType() EventType {
	return EventMCPServerUnavailable
}

// MCPServerStarted records that a configured MCP server came up, and under which sandbox tier.
// Tier is the sandbox package's tier name as-is; "none" means the server runs unconfined.
type MCPServerStarted struct {
	EventHeader
	Server string `json:"server"`
	Tier   string `json:"tier"`
}

func (MCPServerStarted) ReportEventType() EventType {
	return EventMCPServerStarted
}

// FileChanged records that the broker wrote a file for a tool call. Path is workspace-relative
// with forward slashes, for display; the broker's session log holds the authoritative record.
type FileChanged struct {
	EventHeader
	CallID ID     `json:"call_id,omitempty"`
	Path   string `json:"path"`
}

func (FileChanged) ReportEventType() EventType {
	return EventFileChanged
}

// FilesReverted records that /undo put files back to how they were before FromTurn.
type FilesReverted struct {
	EventHeader
	FromTurn ID          `json:"from_turn"`
	Files    FileReverts `json:"files"`
}

func (FilesReverted) ReportEventType() EventType {
	return EventFilesReverted
}

// ConversationRewound records that /undo took FromTurn and every later turn out of the
// conversation. Together with FilesReverted it is what undo_rate counts (6B).
type ConversationRewound struct {
	EventHeader
	FromTurn ID         `json:"from_turn"`
	Mode     RewindMode `json:"mode"`
}

func (ConversationRewound) ReportEventType() EventType {
	return EventConversationRewound
}

// SpeechRequested records that the assistant marked Text to be spoken aloud (§5.11). It is
// shown in the transcript like any other text; speaking it is a voice frontend's choice.
type SpeechRequested struct {
	EventHeader
	Text string `json:"text"`
}

func (SpeechRequested) ReportEventType() EventType {
	return EventSpeechRequested
}
