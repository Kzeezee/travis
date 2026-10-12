package loop_test

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/Kzeezee/travis/internal/agent/loop"
	"github.com/Kzeezee/travis/internal/core"
)

var errLogUnavailable = errors.New("log unavailable")

// recordingLog keeps what the loop appends; failAfter > 0 refuses every append after that many.
type recordingLog struct {
	entries           []core.SessionEntry
	failAfter         int
	wasCancelledOnAdd []bool
}

func (l *recordingLog) AppendEntries(ctx context.Context, entries []core.SessionEntry) error {
	if l.failAfter > 0 && len(l.entries) >= l.failAfter {
		return errLogUnavailable
	}
	l.wasCancelledOnAdd = append(l.wasCancelledOnAdd, ctx.Err() != nil)
	l.entries = append(l.entries, entries...)
	return nil
}

func (l *recordingLog) ListTurns(context.Context) ([]core.TurnSummary, error) { return nil, nil }

func (l *recordingLog) reportRoles() []core.Role {
	roles := make([]core.Role, 0, len(l.entries))
	for _, entry := range l.entries {
		roles = append(roles, entry.Message.Role)
	}
	return roles
}

func buildLoggedConversation(provider core.Provider, tools loop.ToolLookup, log core.SessionLog) *loop.AssistantConversation {
	deps := loop.Dependencies{
		Provider: provider,
		Tools:    tools,
		Events:   &recordingSink{},
		Log:      log,
		Clock:    core.FixedClock{Time: time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)},
		IDs:      &core.SequentialIDGenerator{Prefix: "id-"},
	}
	return loop.BuildAssistantConversation(deps, "test-model", testMaxSteps)
}

func TestRunRecordsEveryMessageOfTheTurnInOrder(t *testing.T) {
	echo := &echoTool{}
	provider := &fakeProvider{streams: []*fakeStream{
		buildToolCallStream(core.ToolCall{ID: "call-1", Name: "echo", Arguments: json.RawMessage(`{"text":"pong"}`)}),
		buildTextStream(core.Chunk{Text: "done"}),
	}}
	log := &recordingLog{}
	if err := buildLoggedConversation(provider, &fakeToolLookup{tools: []core.Tool{echo}}, log).RunAssistantTurn(context.Background(), "ping"); err != nil {
		t.Fatal(err)
	}

	want := []core.Role{core.RoleUser, core.RoleAssistant, core.RoleTool, core.RoleAssistant}
	if got := log.reportRoles(); !slices.Equal(got, want) {
		t.Fatalf("recorded roles = %v, want %v", got, want)
	}
	for _, entry := range log.entries {
		if entry.Kind != core.EntryMessage || entry.Turn != log.entries[0].Turn || entry.Turn == "" {
			t.Fatalf("entry %+v is not a message of the turn %q", entry, log.entries[0].Turn)
		}
	}
	if echo.seenTurnID != log.entries[0].Turn {
		t.Fatalf("tool saw turn %q, want %q so the broker files its writes under it", echo.seenTurnID, log.entries[0].Turn)
	}
}

func TestRunEndsTheTurnWhenTheLogRefusesAMessage(t *testing.T) {
	provider := &fakeProvider{streams: []*fakeStream{buildTextStream(core.Chunk{Text: "hi"})}}
	log := &recordingLog{failAfter: 1}
	conversation := buildLoggedConversation(provider, nil, log)

	if err := conversation.RunAssistantTurn(context.Background(), "hello"); !errors.Is(err, errLogUnavailable) {
		t.Fatalf("RunAssistantTurn = %v, want the log's error", err)
	}

	log.failAfter = 0
	provider.streams = []*fakeStream{buildTextStream(core.Chunk{Text: "again"})}
	if err := conversation.RunAssistantTurn(context.Background(), "next"); err != nil {
		t.Fatal(err)
	}
	if messages := provider.requests[1].Messages; len(messages) != 2 {
		t.Fatalf("next request has %d messages, want the two the log accepted: an unrecorded reply is not history", len(messages))
	}
}

func TestRunStopsBeforeTheProviderWhenTheUserMessageCannotBeRecorded(t *testing.T) {
	provider := &fakeProvider{}
	if err := buildLoggedConversation(provider, nil, refusingLog{}).RunAssistantTurn(context.Background(), "hello"); !errors.Is(err, errLogUnavailable) {
		t.Fatalf("RunAssistantTurn = %v, want the log's error", err)
	}
	if len(provider.requests) != 0 {
		t.Fatal("the model was asked about a message the log never recorded")
	}
}

type refusingLog struct{}

func (refusingLog) AppendEntries(context.Context, []core.SessionEntry) error {
	return errLogUnavailable
}
func (refusingLog) ListTurns(context.Context) ([]core.TurnSummary, error) { return nil, nil }

// cancellingTool cancels the turn while it runs, as Esc does mid tool call.
type cancellingTool struct{ cancel context.CancelFunc }

func (cancellingTool) Name() string            { return "slow" }
func (cancellingTool) Describe() core.ToolSpec { return core.ToolSpec{Name: "slow"} }
func (t cancellingTool) Invoke(ctx context.Context, _ json.RawMessage) (core.ToolOutput, error) {
	t.cancel()
	return core.ToolOutput{}, ctx.Err()
}

// A cancelled turn's tool results are still recorded, so the next request never carries a tool
// call without its result.
func TestRunRecordsToolResultsOfACancelledTurn(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	provider := &fakeProvider{streams: []*fakeStream{buildToolCallStream(core.ToolCall{ID: "call-1", Name: "slow"})}}
	log := &recordingLog{}
	err := buildLoggedConversation(provider, &fakeToolLookup{tools: []core.Tool{cancellingTool{cancel: cancel}}}, log).RunAssistantTurn(ctx, "go")
	if err == nil {
		t.Fatal("RunAssistantTurn = nil, want the cancellation")
	}
	if roles := log.reportRoles(); len(roles) != 3 || roles[2] != core.RoleTool {
		t.Fatalf("recorded roles = %v, want user, assistant, tool", roles)
	}
	if log.wasCancelledOnAdd[2] {
		t.Fatal("the tool results were recorded under the cancelled context")
	}
}

// refusingToolResultsLog refuses only tool messages, as the broker does one too large for a frame.
type refusingToolResultsLog struct{ recordingLog }

func (l *refusingToolResultsLog) AppendEntries(ctx context.Context, entries []core.SessionEntry) error {
	if entries[0].Message.Role == core.RoleTool {
		return errLogUnavailable
	}
	return l.recordingLog.AppendEntries(ctx, entries)
}

// Tool results the log refused stay in the history: without them every later request would carry
// tool calls with no answer, which the provider rejects.
func TestToolResultsTheLogRefusesStillAnswerTheirCalls(t *testing.T) {
	echo := &echoTool{}
	provider := &fakeProvider{streams: []*fakeStream{
		buildToolCallStream(core.ToolCall{ID: "call-1", Name: "echo", Arguments: json.RawMessage(`{"text":"pong"}`)}),
	}}
	conversation := buildLoggedConversation(provider, &fakeToolLookup{tools: []core.Tool{echo}}, &refusingToolResultsLog{})

	if err := conversation.RunAssistantTurn(context.Background(), "ping"); !errors.Is(err, errLogUnavailable) {
		t.Fatalf("RunAssistantTurn = %v, want the log's refusal to end the turn", err)
	}
	provider.streams = []*fakeStream{buildTextStream(core.Chunk{Text: "ok"})}
	if err := conversation.RunAssistantTurn(context.Background(), "next"); err != nil {
		t.Fatal(err)
	}
	history := provider.requests[1].Messages
	if len(history) != 4 || history[2].Role != core.RoleTool {
		t.Fatalf("next request = %+v, want user, assistant call, its tool result, user", history)
	}
}
