package loop_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"testing"
	"time"

	"github.com/Kzeezee/travis/internal/agent/loop"
	"github.com/Kzeezee/travis/internal/core"
)

var (
	errProviderUnavailable = errors.New("provider unavailable")
	errToolBroken          = errors.New("tool broken")
)

const testMaxSteps = 4

type fakeStream struct {
	chunks []core.Chunk
	failAt int // index at which Next returns errProviderUnavailable; negative disables
	next   int
	closed bool
}

func (s *fakeStream) Next() (core.Chunk, error) {
	if s.failAt >= 0 && s.next == s.failAt {
		return core.Chunk{}, errProviderUnavailable
	}
	if s.next >= len(s.chunks) {
		return core.Chunk{}, io.EOF
	}
	chunk := s.chunks[s.next]
	s.next++
	return chunk, nil
}

func (s *fakeStream) Close() error {
	s.closed = true
	return nil
}

func buildTextStream(chunks ...core.Chunk) *fakeStream {
	return &fakeStream{failAt: -1, chunks: chunks}
}

func buildToolCallStream(calls ...core.ToolCall) *fakeStream {
	return buildTextStream(core.Chunk{ToolCalls: calls, Finish: core.StopToolCalls})
}

// fakeProvider serves queued streams, one per Chat call.
type fakeProvider struct {
	streams  []*fakeStream
	chatErr  error
	requests []core.ProviderRequest
}

func (p *fakeProvider) Chat(_ context.Context, request core.ProviderRequest) (core.Stream, error) {
	p.requests = append(p.requests, request)
	if p.chatErr != nil {
		return nil, p.chatErr
	}
	if len(p.streams) == 0 {
		return buildTextStream(), nil
	}
	stream := p.streams[0]
	p.streams = p.streams[1:]
	return stream, nil
}

type recordingSink struct {
	events []core.Event
}

func (s *recordingSink) Publish(_ context.Context, event core.Event) {
	s.events = append(s.events, event)
}

func (s *recordingSink) types() []core.EventType {
	types := make([]core.EventType, 0, len(s.events))
	for _, event := range s.events {
		types = append(types, event.ReportEventType())
	}
	return types
}

func (s *recordingSink) findToolResults() []core.ToolResult {
	var results []core.ToolResult
	for _, event := range s.events {
		if finished, ok := event.(core.ToolCallFinished); ok {
			results = append(results, finished.Result)
		}
	}
	return results
}

// echoTool strictly decodes {"text": "..."} and answers with the text.
type echoTool struct {
	invocations int
}

func (*echoTool) Name() string { return "echo" }

func (*echoTool) Describe() core.ToolSpec {
	return core.ToolSpec{Name: "echo", Description: "repeats text", InputSchema: json.RawMessage(`{"type":"object"}`)}
}

func (t *echoTool) Invoke(_ context.Context, arguments json.RawMessage) (core.ToolOutput, error) {
	t.invocations++
	var input struct {
		Text string `json:"text"`
	}
	decoder := json.NewDecoder(bytes.NewReader(arguments))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		return core.ToolOutput{Content: `{"error":"invalid_arguments"}`, Display: "invalid arguments", IsError: true}, nil
	}
	return core.ToolOutput{Content: input.Text, Display: "echoed " + input.Text}, nil
}

type panickingTool struct{}

func (panickingTool) Name() string            { return "panic" }
func (panickingTool) Describe() core.ToolSpec { return core.ToolSpec{Name: "panic"} }
func (panickingTool) Invoke(context.Context, json.RawMessage) (core.ToolOutput, error) {
	panic("boom")
}

type failingTool struct{}

func (failingTool) Name() string            { return "fail" }
func (failingTool) Describe() core.ToolSpec { return core.ToolSpec{Name: "fail"} }
func (failingTool) Invoke(context.Context, json.RawMessage) (core.ToolOutput, error) {
	return core.ToolOutput{}, errToolBroken
}

type fakeToolLookup struct {
	tools []core.Tool
}

func (l *fakeToolLookup) Prune(context.Context, int, string) []core.Tool {
	return l.tools
}

func (l *fakeToolLookup) Lookup(name string) (core.Tool, bool) {
	for _, tool := range l.tools {
		if tool.Name() == name {
			return tool, true
		}
	}
	return nil, false
}

func buildAssistantConversation(t *testing.T, provider core.Provider, tools loop.ToolLookup, sink core.EventSink) *loop.AssistantConversation {
	t.Helper()
	deps := loop.Dependencies{
		Provider: provider,
		Tools:    tools,
		Events:   sink,
		Clock:    core.FixedClock{Time: time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)},
		IDs:      &core.SequentialIDGenerator{Prefix: "id-"},
	}
	return loop.BuildAssistantConversation(deps, "test-model", testMaxSteps)
}

func TestRunStreamsTextAndCompletesTheTurn(t *testing.T) {
	stream := buildTextStream(
		core.Chunk{Text: "hel"},
		core.Chunk{Text: "lo"},
		core.Chunk{Usage: &core.Usage{InputTokens: 7, OutputTokens: 2}, Finish: core.StopEndTurn},
	)
	provider := &fakeProvider{streams: []*fakeStream{stream}}
	sink := &recordingSink{}

	if err := buildAssistantConversation(t, provider, nil, sink).RunAssistantTurn(context.Background(), "hi"); err != nil {
		t.Fatalf("RunAssistantTurn returned %v, want nil", err)
	}

	assertEventTypes(t, sink, []core.EventType{
		core.EventUserMessageSubmitted,
		core.EventAssistantTextStreamed,
		core.EventAssistantTextStreamed,
		core.EventTurnCompleted,
	})
	completed := assertTurnCompleted(t, sink, core.StopEndTurn)
	if completed.Usage.OutputTokens != 2 {
		t.Fatalf("got output tokens %d, want 2", completed.Usage.OutputTokens)
	}
	if !stream.closed {
		t.Fatal("stream was left open")
	}
}

func TestRunSendsPriorTurnsAsContext(t *testing.T) {
	provider := &fakeProvider{streams: []*fakeStream{
		buildTextStream(core.Chunk{Text: "first reply"}),
		buildTextStream(core.Chunk{Text: "second reply"}),
	}}
	assistantConversation := buildAssistantConversation(t, provider, nil, &recordingSink{})

	if err := assistantConversation.RunAssistantTurn(context.Background(), "first"); err != nil {
		t.Fatalf("first RunAssistantTurn returned %v, want nil", err)
	}
	if err := assistantConversation.RunAssistantTurn(context.Background(), "second"); err != nil {
		t.Fatalf("second RunAssistantTurn returned %v, want nil", err)
	}

	if len(provider.requests) != 2 {
		t.Fatalf("got %d provider requests, want 2", len(provider.requests))
	}
	second := provider.requests[1].Messages
	if len(second) != 3 {
		t.Fatalf("got %d messages on the second turn, want user, assistant, user", len(second))
	}
	if second[0].Role != core.RoleUser || second[1].Role != core.RoleAssistant || second[2].Role != core.RoleUser {
		t.Fatalf("got roles %q/%q/%q, want user/assistant/user", second[0].Role, second[1].Role, second[2].Role)
	}
	replyPart, ok := second[1].Parts[0].(core.TextPart)
	if !ok || replyPart.Text != "first reply" {
		t.Fatalf("got recorded reply %v, want the streamed text", second[1].Parts[0])
	}
}

func TestRunReportsAFailedProviderCall(t *testing.T) {
	provider := &fakeProvider{chatErr: errProviderUnavailable}
	sink := &recordingSink{}

	err := buildAssistantConversation(t, provider, nil, sink).RunAssistantTurn(context.Background(), "hi")

	if !errors.Is(err, errProviderUnavailable) {
		t.Fatalf("got %v, want errProviderUnavailable", err)
	}
	assertTurnCompleted(t, sink, core.StopError)
}

func TestRunReportsAFailureMidStream(t *testing.T) {
	provider := &fakeProvider{streams: []*fakeStream{{failAt: 1, chunks: []core.Chunk{{Text: "partial"}}}}}
	sink := &recordingSink{}

	err := buildAssistantConversation(t, provider, nil, sink).RunAssistantTurn(context.Background(), "hi")

	if !errors.Is(err, errProviderUnavailable) {
		t.Fatalf("got %v, want errProviderUnavailable", err)
	}
	assertTurnCompleted(t, sink, core.StopError)
}

func TestRunDispatchesAToolCallAndSendsTheResultBack(t *testing.T) {
	echo := &echoTool{}
	firstStream := buildToolCallStream(core.ToolCall{ID: "call-1", Name: "echo", Arguments: json.RawMessage(`{"text":"pong"}`)})
	provider := &fakeProvider{streams: []*fakeStream{firstStream, buildTextStream(core.Chunk{Text: "done"})}}
	sink := &recordingSink{}

	err := buildAssistantConversation(t, provider, &fakeToolLookup{tools: []core.Tool{echo}}, sink).
		RunAssistantTurn(context.Background(), "ping")
	if err != nil {
		t.Fatalf("RunAssistantTurn returned %v, want nil", err)
	}

	if echo.invocations != 1 {
		t.Fatalf("got %d invocations, want 1", echo.invocations)
	}
	if !firstStream.closed {
		t.Fatal("first step's stream was left open")
	}
	if len(provider.requests) != 2 {
		t.Fatalf("got %d provider requests, want 2", len(provider.requests))
	}

	second := provider.requests[1].Messages
	if len(second) != 3 {
		t.Fatalf("got %d messages on the second request, want user, assistant, tool", len(second))
	}
	callPart, ok := second[1].Parts[0].(core.ToolCallPart)
	if second[1].Role != core.RoleAssistant || !ok || callPart.ToolCall.ID != "call-1" {
		t.Fatalf("got assistant message %+v, want the tool call", second[1])
	}
	resultPart, ok := second[2].Parts[0].(core.ToolResultPart)
	if second[2].Role != core.RoleTool || !ok {
		t.Fatalf("got message %+v, want a tool result", second[2])
	}
	want := core.ToolResult{CallID: "call-1", Name: "echo", Content: "pong"}
	if resultPart.ToolResult != want {
		t.Fatalf("got result %+v, want %+v", resultPart.ToolResult, want)
	}

	assertEventTypes(t, sink, []core.EventType{
		core.EventUserMessageSubmitted,
		core.EventToolCallStarted,
		core.EventToolCallFinished,
		core.EventAssistantTextStreamed,
		core.EventTurnCompleted,
	})
	finished, _ := sink.events[2].(core.ToolCallFinished)
	if finished.Display != "echoed pong" {
		t.Fatalf("got display %q, want the tool's display text", finished.Display)
	}
	assertTurnCompleted(t, sink, core.StopEndTurn)
}

func TestRunGivesEveryEventAnID(t *testing.T) {
	provider := &fakeProvider{streams: []*fakeStream{
		buildToolCallStream(core.ToolCall{ID: "call-1", Name: "echo", Arguments: json.RawMessage(`{"text":"a"}`)}),
		buildTextStream(core.Chunk{Text: "done"}),
	}}
	sink := &recordingSink{}

	err := buildAssistantConversation(t, provider, &fakeToolLookup{tools: []core.Tool{&echoTool{}}}, sink).
		RunAssistantTurn(context.Background(), "go")
	if err != nil {
		t.Fatalf("RunAssistantTurn returned %v, want nil", err)
	}

	seen := make(map[core.ID]bool)
	for _, event := range sink.events {
		id := reportEventID(t, event)
		if id == "" {
			t.Fatalf("got %T with an empty ID", event)
		}
		if seen[id] {
			t.Fatalf("got duplicate event ID %q", id)
		}
		seen[id] = true
	}
}

func TestRunMintsAnIDForACallWithoutOne(t *testing.T) {
	provider := &fakeProvider{streams: []*fakeStream{
		buildToolCallStream(core.ToolCall{Name: "echo", Arguments: json.RawMessage(`{"text":"a"}`)}),
		buildTextStream(core.Chunk{Text: "done"}),
	}}
	sink := &recordingSink{}

	err := buildAssistantConversation(t, provider, &fakeToolLookup{tools: []core.Tool{&echoTool{}}}, sink).
		RunAssistantTurn(context.Background(), "go")
	if err != nil {
		t.Fatalf("RunAssistantTurn returned %v, want nil", err)
	}

	callPart, _ := provider.requests[1].Messages[1].Parts[0].(core.ToolCallPart)
	resultPart, _ := provider.requests[1].Messages[2].Parts[0].(core.ToolResultPart)
	if callPart.ToolCall.ID == "" {
		t.Fatal("got a tool call with no ID, want a minted one")
	}
	if resultPart.ToolResult.CallID != callPart.ToolCall.ID {
		t.Fatalf("got result for %q, want it matched to call %q", resultPart.ToolResult.CallID, callPart.ToolCall.ID)
	}
}

func TestRunAnswersAnUnknownToolWithAnErrorAndContinues(t *testing.T) {
	provider := &fakeProvider{streams: []*fakeStream{
		buildToolCallStream(core.ToolCall{ID: "call-1", Name: "missing"}),
		buildTextStream(core.Chunk{Text: "sorry"}),
	}}
	sink := &recordingSink{}

	err := buildAssistantConversation(t, provider, &fakeToolLookup{tools: []core.Tool{&echoTool{}}}, sink).
		RunAssistantTurn(context.Background(), "go")
	if err != nil {
		t.Fatalf("RunAssistantTurn returned %v, want nil", err)
	}

	assertSingleErrorResult(t, sink, `{"error":"unknown_tool"}`)
	assertTurnCompleted(t, sink, core.StopEndTurn)
}

func TestRunAnswersMalformedArgumentsWithTheToolsError(t *testing.T) {
	provider := &fakeProvider{streams: []*fakeStream{
		buildToolCallStream(core.ToolCall{ID: "call-1", Name: "echo", Arguments: json.RawMessage(`{"text":`)}),
		buildTextStream(core.Chunk{Text: "retrying"}),
	}}
	sink := &recordingSink{}

	err := buildAssistantConversation(t, provider, &fakeToolLookup{tools: []core.Tool{&echoTool{}}}, sink).
		RunAssistantTurn(context.Background(), "go")
	if err != nil {
		t.Fatalf("RunAssistantTurn returned %v, want nil", err)
	}

	assertSingleErrorResult(t, sink, `{"error":"invalid_arguments"}`)
	assertTurnCompleted(t, sink, core.StopEndTurn)
}

func TestRunSurvivesAPanickingTool(t *testing.T) {
	provider := &fakeProvider{streams: []*fakeStream{
		buildToolCallStream(core.ToolCall{ID: "call-1", Name: "panic"}),
		buildTextStream(core.Chunk{Text: "recovered"}),
	}}
	sink := &recordingSink{}

	err := buildAssistantConversation(t, provider, &fakeToolLookup{tools: []core.Tool{panickingTool{}}}, sink).
		RunAssistantTurn(context.Background(), "go")
	if err != nil {
		t.Fatalf("RunAssistantTurn returned %v, want nil", err)
	}

	assertSingleErrorResult(t, sink, `{"error":"tool_panicked"}`)
	assertTurnCompleted(t, sink, core.StopEndTurn)
}

func TestRunEndsTheTurnWhenAToolReturnsAnError(t *testing.T) {
	provider := &fakeProvider{streams: []*fakeStream{
		buildToolCallStream(
			core.ToolCall{ID: "call-1", Name: "fail"},
			core.ToolCall{ID: "call-2", Name: "echo", Arguments: json.RawMessage(`{"text":"a"}`)},
		),
		buildTextStream(core.Chunk{Text: "next turn"}),
	}}
	echo := &echoTool{}
	sink := &recordingSink{}
	assistantConversation := buildAssistantConversation(t, provider, &fakeToolLookup{tools: []core.Tool{failingTool{}, echo}}, sink)

	err := assistantConversation.RunAssistantTurn(context.Background(), "go")

	if !errors.Is(err, errToolBroken) {
		t.Fatalf("got %v, want errToolBroken", err)
	}
	if echo.invocations != 0 {
		t.Fatalf("got %d invocations after the failure, want 0", echo.invocations)
	}
	assertTurnCompleted(t, sink, core.StopError)

	// The next turn's history must still answer both calls.
	if err := assistantConversation.RunAssistantTurn(context.Background(), "again"); err != nil {
		t.Fatalf("next RunAssistantTurn returned %v, want nil", err)
	}
	toolMessage := provider.requests[1].Messages[2]
	if toolMessage.Role != core.RoleTool || len(toolMessage.Parts) != 2 {
		t.Fatalf("got %+v, want a tool message answering both calls", toolMessage)
	}
}

func TestRunStopsWhenTheStepBudgetRunsOut(t *testing.T) {
	streams := make([]*fakeStream, 0, testMaxSteps+1)
	for range testMaxSteps + 1 {
		streams = append(streams, buildToolCallStream(core.ToolCall{ID: "call", Name: "echo", Arguments: json.RawMessage(`{"text":"a"}`)}))
	}
	provider := &fakeProvider{streams: streams}
	sink := &recordingSink{}

	err := buildAssistantConversation(t, provider, &fakeToolLookup{tools: []core.Tool{&echoTool{}}}, sink).
		RunAssistantTurn(context.Background(), "loop forever")
	if err != nil {
		t.Fatalf("RunAssistantTurn returned %v, want nil", err)
	}

	if len(provider.requests) != testMaxSteps {
		t.Fatalf("got %d provider requests, want %d", len(provider.requests), testMaxSteps)
	}
	assertTurnCompleted(t, sink, core.StopBudget)
}

func TestRunOffersTheLookupsToolsToTheModel(t *testing.T) {
	provider := &fakeProvider{streams: []*fakeStream{buildTextStream(core.Chunk{Text: "hi"})}}

	err := buildAssistantConversation(t, provider, &fakeToolLookup{tools: []core.Tool{&echoTool{}}}, &recordingSink{}).
		RunAssistantTurn(context.Background(), "hi")
	if err != nil {
		t.Fatalf("RunAssistantTurn returned %v, want nil", err)
	}

	tools := provider.requests[0].Tools
	if len(tools) != 1 || tools[0].Name != "echo" {
		t.Fatalf("got tools %+v, want the echo spec", tools)
	}
}

func TestRunOffersNoToolsWithoutALookup(t *testing.T) {
	provider := &fakeProvider{streams: []*fakeStream{buildTextStream(core.Chunk{Text: "hi"})}}

	if err := buildAssistantConversation(t, provider, nil, &recordingSink{}).RunAssistantTurn(context.Background(), "hi"); err != nil {
		t.Fatalf("RunAssistantTurn returned %v, want nil", err)
	}

	if tools := provider.requests[0].Tools; len(tools) != 0 {
		t.Fatalf("got tools %+v, want none", tools)
	}
}

func assertEventTypes(t *testing.T, sink *recordingSink, want []core.EventType) {
	t.Helper()
	got := sink.types()
	if len(got) != len(want) {
		t.Fatalf("got events %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got events %v, want %v", got, want)
		}
	}
}

func assertTurnCompleted(t *testing.T, sink *recordingSink, reason core.StopReason) core.TurnCompleted {
	t.Helper()
	last := sink.events[len(sink.events)-1]
	completed, ok := last.(core.TurnCompleted)
	if !ok {
		t.Fatalf("got %T as the final event, want core.TurnCompleted", last)
	}
	if completed.Reason != reason {
		t.Fatalf("got reason %q, want %q", completed.Reason, reason)
	}
	return completed
}

func assertSingleErrorResult(t *testing.T, sink *recordingSink, content string) {
	t.Helper()
	results := sink.findToolResults()
	if len(results) != 1 {
		t.Fatalf("got %d tool results, want 1", len(results))
	}
	if !results[0].IsError || results[0].Content != content {
		t.Fatalf("got result %+v, want error %s", results[0], content)
	}
}

func reportEventID(t *testing.T, event core.Event) core.ID {
	t.Helper()
	switch fact := event.(type) {
	case core.UserMessageSubmitted:
		return fact.ID
	case core.AssistantTextStreamed:
		return fact.ID
	case core.ToolCallStarted:
		return fact.ID
	case core.ToolCallFinished:
		return fact.ID
	case core.TurnCompleted:
		return fact.ID
	default:
		t.Fatalf("got unexpected event %T", event)
		return ""
	}
}
