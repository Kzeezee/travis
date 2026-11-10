package terminal

import (
	"slices"
	"strings"
	"testing"

	"github.com/Kzeezee/travis/internal/core"
)

func TestTranscriptKeepsTextAndToolCardsInArrivalOrder(t *testing.T) {
	var history transcript
	history.startTurn("list my notes")

	for _, event := range []core.Event{
		core.UserMessageSubmitted{},
		core.AssistantTextStreamed{Text: "Let me "},
		core.AssistantTextStreamed{Text: "look."},
		core.ToolCallStarted{Call: core.ToolCall{ID: "a", Name: "list_dir"}},
		core.ToolCallStarted{Call: core.ToolCall{ID: "b", Name: "read_file"}},
		core.ToolCallFinished{Result: core.ToolResult{CallID: "b", Name: "read_file", Content: "denied\nstack", IsError: true}},
		core.ToolCallFinished{Result: core.ToolResult{CallID: "a", Name: "list_dir"}, Display: "listed docs/notes"},
		core.ToolCallFinished{Result: core.ToolResult{CallID: "zz", Name: "fetch"}},
		core.AssistantTextStreamed{Text: "Done."},
	} {
		history.applyEvent(event)
	}

	parts := history.turns[0].parts
	if len(parts) != 5 {
		t.Fatalf("got %d parts, want text, 3 cards, text", len(parts))
	}
	if parts[0].text != "Let me look." || parts[4].text != "Done." {
		t.Fatalf("got text parts %q and %q, want merged chunks either side of the cards", parts[0].text, parts[4].text)
	}
	if card := parts[1].tool; card.status != toolSucceeded || card.detail != "listed docs/notes" {
		t.Fatalf("got first card %+v, want list_dir succeeded with its display text", *card)
	}
	// No Display, so the model-facing content is the only summary left to show.
	if card := parts[2].tool; card.status != toolFailed || card.detail != "denied" {
		t.Fatalf("got second card %+v, want read_file failed with its first line", *card)
	}
	if card := parts[3].tool; card.name != "fetch" || card.status != toolSucceeded {
		t.Fatalf("got unmatched result %+v, want its own settled card", *card)
	}
}

func TestTranscriptKeepsThoughtsApartFromAnswerText(t *testing.T) {
	var history transcript
	history.startTurn("when is the dentist?")

	for _, event := range []core.Event{
		core.UserMessageSubmitted{},
		core.ThoughtStreamed{Text: "Search "},
		core.ThoughtStreamed{Text: "notes."},
		core.AssistantTextStreamed{Text: "On the 13th."},
		core.ThoughtStreamed{Text: "Done."},
	} {
		history.applyEvent(event)
	}

	want := []turnPart{{text: "Search notes.", isThought: true}, {text: "On the 13th."}, {text: "Done.", isThought: true}}
	if got := history.turns[0].parts; !slices.Equal(got, want) {
		t.Fatalf("got parts %+v, want %+v: thoughts merged with each other, never with the answer", got, want)
	}
}

func TestTranscriptRoutesEventsToTheTurnThatProducedThem(t *testing.T) {
	var history transcript
	history.applyEvent(core.AssistantTextStreamed{Text: "before any turn"})
	history.startTurn("one")
	history.applyEvent(core.UserMessageSubmitted{})
	history.startTurn("two") // submitted before turn one's late chunk is observed
	history.applyEvent(core.AssistantTextStreamed{Text: "late for one"})
	history.applyEvent(core.UserMessageSubmitted{})
	history.applyEvent(core.AssistantTextStreamed{Text: "for two"})

	if got := history.turns[0].parts[0].text; got != "late for one" {
		t.Fatalf("got turn one text %q, want the late chunk", got)
	}
	if got := history.turns[1].parts[0].text; got != "for two" {
		t.Fatalf("got turn two text %q, want only its own chunk", got)
	}
}

func TestTranscriptTotalsUsageAndKeepsItAcrossReset(t *testing.T) {
	var history transcript
	history.startTurn("hi")
	history.applyEvent(core.UserMessageSubmitted{})
	history.applyEvent(core.TurnCompleted{Usage: core.Usage{InputTokens: 3, OutputTokens: 4}})
	history.applyEvent(core.TurnCompleted{Usage: core.Usage{InputTokens: 10, OutputTokens: 20}})

	if history.usage != (core.Usage{InputTokens: 13, OutputTokens: 24}) {
		t.Fatalf("got usage %+v, want summed totals", history.usage)
	}

	history.resetTranscript()
	if len(history.turns) != 0 || history.observedTurns != 0 || history.usage.InputTokens != 13 {
		t.Fatalf("got %+v after reset, want empty conversation with usage kept", history)
	}
}

func TestTranscriptExplainsATurnStoppedByItsBudget(t *testing.T) {
	var history transcript
	history.startTurn("hi")
	history.applyEvent(core.UserMessageSubmitted{})
	history.applyEvent(core.TurnCompleted{Reason: core.StopBudget})

	if notices := history.turns[0].notices; len(notices) != 1 || !strings.Contains(notices[0], "limit") {
		t.Fatalf("got notices %q, want one naming the limit", notices)
	}
}

// A resumed session's turns render as finished turns, /undo can hide them by ID, and the next
// turn's events land on the new turn rather than a restored one.
func TestTranscriptRestoresAResumedSessionsTurns(t *testing.T) {
	message := func(role core.Role, parts ...core.Part) core.Message { return core.Message{Role: role, Parts: parts} }
	var history transcript
	history.restoreTurns([]RestoredMessage{
		{Turn: "t1", Message: message(core.RoleUser, core.TextPart{Text: "read it"})},
		{Turn: "t1", Message: message(core.RoleAssistant, core.ToolCallPart{ToolCall: core.ToolCall{ID: "c1", Name: "fs_read"}})},
		{Turn: "t1", Message: message(core.RoleTool, core.ToolResultPart{ToolResult: core.ToolResult{CallID: "c1", Name: "fs_read"}})},
		{Turn: "t1", Message: message(core.RoleAssistant, core.TextPart{Text: "It says hi."})},
		{Turn: "t2", Message: message(core.RoleUser, core.TextPart{Text: "thanks"})},
	})

	if len(history.turns) != 2 || history.turns[0].prompt != "read it" || history.turns[1].prompt != "thanks" {
		t.Fatalf("restored turns = %+v", history.turns)
	}
	first := history.turns[0]
	if len(first.parts) != 2 || first.parts[0].tool.status != toolSucceeded || first.parts[1].text != "It says hi." {
		t.Fatalf("first turn parts = %+v, want a settled tool card then the reply", first.parts)
	}

	history.startTurn("next")
	history.applyEvent(core.UserMessageSubmitted{EventHeader: core.EventHeader{TurnID: "t3"}})
	history.applyEvent(core.AssistantTextStreamed{Text: "new reply"})
	if got := history.turns[2]; got.turnID != "t3" || len(got.parts) != 1 {
		t.Fatalf("new turn = %+v, want the streamed reply on it", got)
	}

	if prompt, isFound := history.rewindTurns("t2", "rewound"); !isFound || prompt != "thanks" || !history.turns[1].isRewound {
		t.Fatalf("rewind of a restored turn = %q, %t", prompt, isFound)
	}
}
