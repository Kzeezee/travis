package terminal

import (
	"cmp"
	"strings"

	"github.com/Kzeezee/travis/internal/core"
)

// transcript keeps chat history as source, never pre-wrapped, so resizing re-wraps it (DESIGN.md §5.9.1).
type transcript struct {
	warnings []string
	turns    []chatTurn
	// observedTurns counts UserMessageSubmitted facts. Streamed events belong to that turn, so a
	// chunk that arrives after its turn finished can never land in the next one.
	observedTurns int
}

type chatTurn struct {
	prompt  string
	parts   []turnPart
	outcome turnOutcome
	failure string
}

// turnPart is assistant text, or a tool card when tool is set; parts keep arrival order.
type turnPart struct {
	text string
	tool *toolCard
}

type toolCard struct {
	callID core.ID
	name   string
	status toolStatus
	detail string
}

type toolStatus int

const (
	toolRunning toolStatus = iota
	toolSucceeded
	toolFailed
)

type turnOutcome int

const (
	turnPending turnOutcome = iota
	turnSucceeded
	turnFailed
	turnInterrupted
)

// startTurn records a submitted prompt and reports its turn number, counting from one.
func (t *transcript) startTurn(prompt string) int {
	t.turns = append(t.turns, chatTurn{prompt: prompt})
	return len(t.turns)
}

func (t *transcript) finishTurn(turnNumber int, outcome turnOutcome, failure string) {
	turn := &t.turns[turnNumber-1]
	turn.outcome, turn.failure = outcome, failure
}

func (t *transcript) applyEvent(event core.Event) {
	if _, isNewTurn := event.(core.UserMessageSubmitted); isNewTurn {
		t.observedTurns++
		return
	}
	if t.observedTurns < 1 || t.observedTurns > len(t.turns) {
		return
	}

	turn := &t.turns[t.observedTurns-1]
	switch fact := event.(type) {
	case core.AssistantTextStreamed:
		turn.appendText(fact.Text)
	case core.ToolCallStarted:
		turn.parts = append(turn.parts, turnPart{tool: &toolCard{callID: fact.Call.ID, name: fact.Call.Name}})
	case core.ToolCallFinished:
		turn.finishToolCard(fact.Result, fact.Display)
	}
}

func (turn *chatTurn) appendText(text string) {
	if last := len(turn.parts) - 1; last >= 0 && turn.parts[last].tool == nil {
		turn.parts[last].text += text
		return
	}
	turn.parts = append(turn.parts, turnPart{text: text})
}

// finishToolCard settles the running card with the result's call ID; a result without one
// still renders, as its own card. Display is the human summary; Content is model-facing, so it is only a fallback.
func (turn *chatTurn) finishToolCard(result core.ToolResult, display string) {
	status, detail := toolSucceeded, display
	if result.IsError {
		status, detail = toolFailed, cmp.Or(display, cutFirstLine(result.Content))
	}

	for _, part := range turn.parts {
		if part.tool != nil && part.tool.callID == result.CallID && part.tool.status == toolRunning {
			part.tool.status, part.tool.detail = status, detail
			return
		}
	}
	card := &toolCard{callID: result.CallID, name: result.Name, status: status, detail: detail}
	turn.parts = append(turn.parts, turnPart{tool: card})
}

func cutFirstLine(text string) string {
	firstLine, _, _ := strings.Cut(strings.TrimSpace(text), "\n")
	return firstLine
}
