package terminal

import (
	"cmp"
	"slices"
	"strings"

	"github.com/Kzeezee/travis/internal/core"
)

// transcript keeps chat history as source, never pre-wrapped, so resizing re-wraps it (DESIGN.md §5.9.1).
type transcript struct {
	warnings []string
	turns    []chatTurn
	// usage totals TurnCompleted facts across the session for /cost.
	usage core.Usage
	// observedTurns counts UserMessageSubmitted facts. Streamed events belong to that turn, so a
	// chunk that arrives after its turn finished can never land in the next one.
	observedTurns int
}

type chatTurn struct {
	// turnID comes from the turn's UserMessageSubmitted; /undo names turns by it.
	turnID  core.ID
	prompt  string
	parts   []turnPart
	outcome turnOutcome
	failure string
	// changedPaths are the files the broker wrote during the turn, for the "changed" line.
	changedPaths []string
	// isRewound turns stay in place, so later events still find their turn, but only their
	// notices render; notices are /undo results shown after (or instead of) the turn.
	isRewound bool
	notices   []string
}

// turnPart is assistant text, model reasoning when isThought, or a tool card when tool is set;
// parts keep arrival order.
type turnPart struct {
	text      string
	isThought bool
	tool      *toolCard
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

// resetTranscript forgets the conversation but keeps startup warnings and the session usage total.
func (t *transcript) resetTranscript() {
	t.turns, t.observedTurns = nil, 0
}

func (t *transcript) finishTurn(turnNumber int, outcome turnOutcome, failure string) {
	turn := &t.turns[turnNumber-1]
	turn.outcome, turn.failure = outcome, failure
}

func (t *transcript) applyEvent(event core.Event) {
	if submitted, isNewTurn := event.(core.UserMessageSubmitted); isNewTurn {
		t.observedTurns++
		if t.observedTurns <= len(t.turns) {
			t.turns[t.observedTurns-1].turnID = submitted.TurnID
		}
		return
	}
	if completed, isCompleted := event.(core.TurnCompleted); isCompleted {
		t.usage.InputTokens += completed.Usage.InputTokens
		t.usage.OutputTokens += completed.Usage.OutputTokens
		return
	}
	if t.observedTurns < 1 || t.observedTurns > len(t.turns) {
		return
	}

	turn := &t.turns[t.observedTurns-1]
	switch fact := event.(type) {
	case core.AssistantTextStreamed:
		turn.appendStreamed(turnPart{text: fact.Text})
	case core.ThoughtStreamed:
		turn.appendStreamed(turnPart{text: fact.Text, isThought: true})
	case core.ToolCallStarted:
		turn.parts = append(turn.parts, turnPart{tool: &toolCard{callID: fact.Call.ID, name: fact.Call.Name}})
	case core.ToolCallFinished:
		turn.finishToolCard(fact.Result, fact.Display)
	case core.FileChanged:
		if !slices.Contains(turn.changedPaths, fact.Path) {
			turn.changedPaths = append(turn.changedPaths, fact.Path)
		}
	}
}

// rewindTurns hides fromTurn and every later turn and leaves notice in their place; it reports
// the rewound turn's prompt so the user can edit and resend it. A turn it cannot find (its
// UserMessageSubmitted was dropped) hides nothing, and the notice goes at the end.
func (t *transcript) rewindTurns(fromTurn core.ID, notice string) (string, bool) {
	start := slices.IndexFunc(t.turns, func(turn chatTurn) bool { return turn.turnID == fromTurn && !turn.isRewound })
	if start < 0 {
		t.addNotice(notice)
		return "", false
	}
	// Earlier notices on these turns describe history that is now gone; the new notice replaces
	// them all, so the transcript never shows an older rewind below a newer one.
	for i := start; i < len(t.turns); i++ {
		t.turns[i].isRewound, t.turns[i].notices = true, nil
	}
	t.turns[start].notices = []string{notice}
	return t.turns[start].prompt, true
}

// addNotice shows notice after the latest turn, or among the warnings before any turn.
func (t *transcript) addNotice(notice string) {
	if len(t.turns) == 0 {
		t.warnings = append(t.warnings, notice)
		return
	}
	last := &t.turns[len(t.turns)-1]
	last.notices = append(last.notices, notice)
}

// appendStreamed merges a chunk into the last part only when both are text of the same kind,
// so reasoning never runs into the answer.
func (turn *chatTurn) appendStreamed(part turnPart) {
	if last := len(turn.parts) - 1; last >= 0 && turn.parts[last].tool == nil && turn.parts[last].isThought == part.isThought {
		turn.parts[last].text += part.text
		return
	}
	turn.parts = append(turn.parts, part)
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
