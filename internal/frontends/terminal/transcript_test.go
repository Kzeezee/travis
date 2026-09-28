package terminal

import (
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
