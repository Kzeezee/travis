package loop_test

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Kzeezee/travis/internal/agent/loop"
	"github.com/Kzeezee/travis/internal/core"
)

// runTextTurns runs one turn per prompt, each answered with "re: <prompt>", and returns their turn ids.
func runTextTurns(t *testing.T, conversation *loop.AssistantConversation, provider *fakeProvider, log *recordingLog, prompts ...string) []core.ID {
	t.Helper()
	var turns []core.ID
	for _, prompt := range prompts {
		provider.streams = append(provider.streams, buildTextStream(core.Chunk{Text: "re: " + prompt}))
		if err := conversation.RunAssistantTurn(context.Background(), prompt); err != nil {
			t.Fatal(err)
		}
		turns = append(turns, log.entries[len(log.entries)-2].Turn)
	}
	return turns
}

func reportTexts(messages []core.Message) []string {
	var texts []string
	for _, message := range messages {
		for _, part := range message.Parts {
			if text, isText := part.(core.TextPart); isText {
				texts = append(texts, text.Text)
			}
		}
	}
	return texts
}

func TestRewindTakesTheTurnAndLaterOnesOutOfTheNextRequest(t *testing.T) {
	provider, log := &fakeProvider{}, &recordingLog{}
	conversation := buildLoggedConversation(provider, nil, log)
	turns := runTextTurns(t, conversation, provider, log, "first", "second", "third")

	if _, err := conversation.Rewind(context.Background(), turns[1], core.RewindFilesAndConversation); err != nil {
		t.Fatal(err)
	}
	runTextTurns(t, conversation, provider, log, "again")

	got := reportTexts(provider.requests[len(provider.requests)-1].Messages)
	if want := []string{"first", "re: first", "again"}; !slices.Equal(got, want) {
		t.Fatalf("request after the rewind = %v, want %v", got, want)
	}
	if log.rewoundFrom != turns[1] {
		t.Fatalf("log asked to rewind from %q, want %q", log.rewoundFrom, turns[1])
	}
}

func TestRewindingTwiceKeepsOnlyWhatCameBeforeBoth(t *testing.T) {
	provider, log := &fakeProvider{}, &recordingLog{}
	conversation := buildLoggedConversation(provider, nil, log)
	turns := runTextTurns(t, conversation, provider, log, "a", "b", "c")
	if _, err := conversation.Rewind(context.Background(), turns[2], core.RewindConversationOnly); err != nil {
		t.Fatal(err)
	}
	later := runTextTurns(t, conversation, provider, log, "d")
	if _, err := conversation.Rewind(context.Background(), turns[1], core.RewindConversationOnly); err != nil {
		t.Fatal(err)
	}
	runTextTurns(t, conversation, provider, log, "e")

	got := reportTexts(provider.requests[len(provider.requests)-1].Messages)
	if want := []string{"a", "re: a", "e"}; !slices.Equal(got, want) {
		t.Fatalf("request = %v, want %v (turn %s came after b, so it went too)", got, want, later[0])
	}
}

// A files-only rewind keeps the conversation, so the model hears which files changed back, once.
func TestFilesOnlyRewindTellsTheModelOnce(t *testing.T) {
	provider := &fakeProvider{}
	log := &recordingLog{report: core.RewindReport{Files: core.FileReverts{
		Restored: []string{"a.go"}, Deleted: []string{"new.go"},
		Skipped: []core.SkippedFile{{Path: "b.go", Reason: "changed since the agent last wrote it"}},
	}}}
	conversation := buildLoggedConversation(provider, nil, log)
	turns := runTextTurns(t, conversation, provider, log, "edit things")

	if _, err := conversation.Rewind(context.Background(), turns[0], core.RewindFilesOnly); err != nil {
		t.Fatal(err)
	}
	runTextTurns(t, conversation, provider, log, "next", "after")

	afterRewind := reportTexts(provider.requests[1].Messages)
	if len(afterRewind) != 4 || afterRewind[0] != "edit things" || afterRewind[3] != "next" {
		t.Fatalf("request after the rewind = %q, want the kept turn, then a note, then the prompt", afterRewind)
	}
	note := afterRewind[2]
	if !strings.HasSuffix(note, "\n\n") {
		t.Fatalf("note %q must end its own paragraph: providers join text parts as they are", note)
	}
	for _, want := range []string{"reverted files", "a.go", "new.go", "b.go (changed since the agent last wrote it)"} {
		if !strings.Contains(note, want) {
			t.Fatalf("note %q does not mention %q", note, want)
		}
	}
	if later := reportTexts(provider.requests[2].Messages); slices.Index(later, note) != 2 || strings.Count(strings.Join(later, "\n"), "reverted files") != 1 {
		t.Fatalf("the note should stay where it was told, once: %q", later)
	}
}

func TestRewindPublishesWhatItDidForTheTrace(t *testing.T) {
	sink := &recordingSink{}
	log := &recordingLog{report: core.RewindReport{Files: core.FileReverts{Restored: []string{"a.go"}}}}
	deps := loop.Dependencies{
		Provider: &fakeProvider{streams: []*fakeStream{buildTextStream(core.Chunk{Text: "ok"})}},
		Events:   sink, Log: log,
		Clock: core.FixedClock{Time: time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)},
		IDs:   &core.SequentialIDGenerator{Prefix: "id-"},
	}
	conversation := loop.BuildAssistantConversation(deps, "test-model", testMaxSteps)
	if err := conversation.RunAssistantTurn(context.Background(), "go"); err != nil {
		t.Fatal(err)
	}
	turn := log.entries[0].Turn

	if _, err := conversation.Rewind(context.Background(), turn, core.RewindFilesAndConversation); err != nil {
		t.Fatal(err)
	}
	types := sink.types()
	if got := types[len(types)-2:]; !slices.Equal(got, []core.EventType{core.EventFilesReverted, core.EventConversationRewound}) {
		t.Fatalf("events = %v, want FilesReverted then ConversationRewound", types)
	}
	reverted := sink.events[len(sink.events)-2].(core.FilesReverted)
	if reverted.FromTurn != turn || len(reverted.Files.Restored) != 1 || reverted.ID == "" {
		t.Fatalf("FilesReverted = %+v", reverted)
	}
}

func TestRewindTheLogRefusesChangesNothing(t *testing.T) {
	provider, log := &fakeProvider{}, &recordingLog{rewindErr: errLogUnavailable}
	conversation := buildLoggedConversation(provider, nil, log)
	turns := runTextTurns(t, conversation, provider, log, "first")

	if _, err := conversation.Rewind(context.Background(), turns[0], core.RewindFilesAndConversation); !errors.Is(err, errLogUnavailable) {
		t.Fatalf("Rewind = %v, want the log's refusal", err)
	}
	runTextTurns(t, conversation, provider, log, "second")
	if got := reportTexts(provider.requests[1].Messages); len(got) != 3 {
		t.Fatalf("request = %v, want the first turn kept: the rewind never happened", got)
	}
}

// rewindingTool tries to rewind the conversation from inside a running turn.
type rewindingTool struct {
	conversation **loop.AssistantConversation
	err          error
}

func (*rewindingTool) Name() string            { return "rewind" }
func (*rewindingTool) Describe() core.ToolSpec { return core.ToolSpec{Name: "rewind"} }
func (r *rewindingTool) Invoke(ctx context.Context, _ json.RawMessage) (core.ToolOutput, error) {
	_, r.err = (*r.conversation).Rewind(ctx, core.ReportTurnID(ctx), core.RewindConversationOnly)
	return core.ToolOutput{Content: "tried"}, nil
}

func TestRewindIsRefusedWhileATurnRuns(t *testing.T) {
	var conversation *loop.AssistantConversation
	tool := &rewindingTool{conversation: &conversation}
	provider := &fakeProvider{streams: []*fakeStream{buildToolCallStream(core.ToolCall{ID: "call-1", Name: "rewind"}), buildTextStream(core.Chunk{Text: "done"})}}
	log := &recordingLog{}
	conversation = buildLoggedConversation(provider, &fakeToolLookup{tools: []core.Tool{tool}}, log)

	if err := conversation.RunAssistantTurn(context.Background(), "go"); err != nil {
		t.Fatal(err)
	}
	if !errors.Is(tool.err, core.ErrInvalid) || log.rewoundFrom != "" {
		t.Fatalf("Rewind mid-turn = %v (log asked from %q), want it refused before reaching the log", tool.err, log.rewoundFrom)
	}
}
