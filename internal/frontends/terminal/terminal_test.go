package terminal

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/Kzeezee/travis/internal/core"
)

func TestRunChatRefusesNonTerminalIO(t *testing.T) {
	runTurn := func(context.Context, string) error {
		t.Fatal("a refused chat must not run a turn")
		return nil
	}
	opts := Options{Input: strings.NewReader("hello\r"), Output: &strings.Builder{}}

	if err := RunChat(context.Background(), nil, runTurn, opts); !errors.Is(err, ErrNotInteractive) {
		t.Fatalf("got error %v, want %v", err, ErrNotInteractive)
	}
}

func TestCollectEventBatchGathersEventsWithinTheWindow(t *testing.T) {
	events := make(chan core.Event, 3)
	events <- core.AssistantTextStreamed{Text: "a"}
	events <- core.AssistantTextStreamed{Text: "b"}
	events <- core.AssistantTextStreamed{Text: "c"}

	batch := collectEventBatch(events, 10*time.Millisecond)

	if len(batch.events) != 3 || batch.isChannelClosed {
		t.Fatalf("got %d events (closed %v), want 3 in one open batch", len(batch.events), batch.isChannelClosed)
	}
}

func TestCollectEventBatchFlushesOnTurnCompleted(t *testing.T) {
	events := make(chan core.Event, 3)
	events <- core.AssistantTextStreamed{Text: "a"}
	events <- core.TurnCompleted{}
	events <- core.AssistantTextStreamed{Text: "next"}

	batch := collectEventBatch(events, time.Hour)

	if len(batch.events) != 2 {
		t.Fatalf("got %d events, want the batch to end at TurnCompleted", len(batch.events))
	}
}

func TestCollectEventBatchReportsAClosedChannel(t *testing.T) {
	events := make(chan core.Event, 1)
	events <- core.AssistantTextStreamed{Text: "last"}
	close(events)

	batch := collectEventBatch(events, time.Hour)

	if len(batch.events) != 1 || !batch.isChannelClosed {
		t.Fatalf("got %d events (closed %v), want the last event and closed", len(batch.events), batch.isChannelClosed)
	}
}

func TestSubmittedPromptRunsATurnAndReportsItsOutcome(t *testing.T) {
	tests := []struct {
		name        string
		turnErr     error
		wantView    string
		wantOutcome string
	}{
		{name: "success", wantView: "you › hello", wantOutcome: "last turn: ok"},
		{name: "failure", turnErr: errors.New("provider unavailable"), wantView: "✗ error: provider unavailable", wantOutcome: "last turn: error"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var gotPrompt string
			m := buildTestModel(func(_ context.Context, prompt string) error {
				gotPrompt = prompt
				return tt.turnErr
			})

			m, _ = pressKeys(m, typedKeys("  hello ")...)
			m, turn := pressKeys(m, enterKey)
			m, _ = updateModel(m, turn())

			if gotPrompt != "hello" {
				t.Fatalf("got prompt %q, want trimmed hello", gotPrompt)
			}
			view := renderPlainView(m)
			if !strings.Contains(view, tt.wantView) || !strings.Contains(view, tt.wantOutcome) {
				t.Fatalf("got view\n%s\nwant %q and %q", view, tt.wantView, tt.wantOutcome)
			}
			if m.input.Value() != "" {
				t.Fatalf("got input %q, want it cleared after submit", m.input.Value())
			}
		})
	}
}

func TestStartupWarningsShowInTheFirstFrame(t *testing.T) {
	m := buildChatModel(context.Background(), make(chan core.Event), nil,
		Options{ModelName: "test-model", StartupWarnings: []string{"config: unknown key brain.max_stepz"}})

	if view := renderPlainView(m); !strings.Contains(view, "⚠ config: unknown key brain.max_stepz") {
		t.Fatalf("got view\n%s\nwant the startup warning", view)
	}
}

func TestCancelKeysInterruptTheRunningTurn(t *testing.T) {
	for _, cancelKey := range []tea.KeyPressMsg{escKey, ctrlKey('c')} {
		t.Run(cancelKey.String(), func(t *testing.T) {
			m := buildTestModel(func(ctx context.Context, _ string) error {
				<-ctx.Done()
				return ctx.Err()
			})
			m, _ = pressKeys(m, typedKeys("hi")...)
			m, turn := pressKeys(m, enterKey)
			finished := make(chan tea.Msg, 1)
			go func() { finished <- turn() }()

			m, cmd := pressKeys(m, cancelKey)
			if cmd != nil {
				t.Fatal("cancelling a turn must not quit")
			}
			if view := renderPlainView(m); !strings.Contains(view, "· interrupted ·") {
				t.Fatalf("got view\n%s\nwant the status to show interrupted immediately", view)
			}

			select {
			case msg := <-finished:
				m, _ = updateModel(m, msg)
			case <-time.After(time.Second):
				t.Fatal("turn did not stop after cancellation")
			}
			if view := renderPlainView(m); !strings.Contains(view, "⊘ interrupted") {
				t.Fatalf("got view\n%s\nwant the turn marked interrupted", view)
			}
		})
	}
}

func TestQuitPaths(t *testing.T) {
	tests := []struct {
		name string
		msgs []tea.Msg
	}{
		{name: "ctrl+c while idle", msgs: []tea.Msg{ctrlKey('c')}},
		{name: "/quit", msgs: append(typedKeys("/quit"), enterKey)},
		{name: "ctrl+d on empty input", msgs: []tea.Msg{ctrlKey('d')}},
		{name: "input ended while idle", msgs: []tea.Msg{inputEndedMsg{}}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := buildTestModel(func(context.Context, string) error {
				t.Fatal("quitting must not run a turn")
				return nil
			})
			_, cmd := pressKeys(m, tt.msgs...)
			if !isQuitCmd(cmd) {
				t.Fatal("want the program to quit")
			}
		})
	}
}

func TestRunningTurnDefersEnterAndInputEnd(t *testing.T) {
	m := buildTestModel(func(context.Context, string) error { return nil })
	m, _ = pressKeys(m, typedKeys("first")...)
	m, turn := pressKeys(m, enterKey)

	m, cmd := pressKeys(m, append(typedKeys("second"), enterKey, inputEndedMsg{})...)
	if cmd != nil {
		t.Fatal("enter and input end must wait while a turn runs")
	}
	if m.input.Value() != "second" {
		t.Fatalf("got input %q, want typed text kept", m.input.Value())
	}
	if _, cmd = updateModel(m, turn()); !isQuitCmd(cmd) {
		t.Fatal("want quit once the running turn finishes after input ended")
	}
}

func TestOnlyScrollKeysAndTheWheelMoveTheTranscript(t *testing.T) {
	m := buildTestModel(func(context.Context, string) error { return nil })
	m, _ = pressKeys(m, typedKeys("long")...)
	m, _ = pressKeys(m, enterKey)
	m, _ = updateModel(m, eventBatchMsg{events: []core.Event{
		core.UserMessageSubmitted{},
		core.AssistantTextStreamed{Text: strings.Repeat("line\n", 100)},
	}})
	bottom := m.viewport.YOffset()

	m, _ = pressKeys(m, typedKeys("jk fbud")...)
	if m.viewport.YOffset() != bottom {
		t.Fatal("typing must not scroll the transcript")
	}
	m, _ = pressKeys(m, tea.KeyPressMsg{Code: tea.KeyPgUp})
	if m.viewport.YOffset() >= bottom {
		t.Fatal("PgUp should scroll the transcript up")
	}
	scrolled := m.viewport.YOffset()
	m, _ = updateModel(m, tea.MouseWheelMsg{Button: tea.MouseWheelDown})
	if m.viewport.YOffset() <= scrolled {
		t.Fatal("the mouse wheel should scroll the transcript")
	}
}

var (
	enterKey = tea.KeyPressMsg{Code: tea.KeyEnter}
	escKey   = tea.KeyPressMsg{Code: tea.KeyEscape}
)

func ctrlKey(code rune) tea.KeyPressMsg {
	return tea.KeyPressMsg{Code: code, Mod: tea.ModCtrl}
}

func typedKeys(text string) []tea.Msg {
	keys := make([]tea.Msg, 0, len(text))
	for _, r := range text {
		keys = append(keys, tea.KeyPressMsg{Code: r, Text: string(r)})
	}
	return keys
}

func buildTestModel(runTurn func(context.Context, string) error) chatModel {
	return buildChatModel(context.Background(), make(chan core.Event), runTurn, Options{ModelName: "test-model"})
}

func updateModel(m chatModel, msg tea.Msg) (chatModel, tea.Cmd) {
	next, cmd := m.Update(msg)
	return next.(chatModel), cmd
}

// pressKeys feeds messages in order and reports the command from the last one.
func pressKeys(m chatModel, msgs ...tea.Msg) (chatModel, tea.Cmd) {
	var cmd tea.Cmd
	for _, msg := range msgs {
		m, cmd = updateModel(m, msg)
	}
	return m, cmd
}

func isQuitCmd(cmd tea.Cmd) bool {
	if cmd == nil {
		return false
	}
	_, isQuit := cmd().(tea.QuitMsg)
	return isQuit
}

func renderPlainView(m chatModel) string {
	return ansi.Strip(m.View().Content)
}

func TestRenderTurnLabelsOnlyRepliesNeverWorkOrBlankText(t *testing.T) {
	turn := chatTurn{prompt: "q", parts: []turnPart{
		{text: "Let me look."},
		{tool: &toolCard{name: "grep", status: toolSucceeded}},
		{text: "\n "},
		{text: "read the note", isThought: true},
		{text: "\nOn the 13th.\n"},
	}}

	got := ansi.Strip(renderTurn(turn, 80))
	if strings.Count(got, "travis ›") != 2 || !strings.Contains(got, "travis › Let me look.") || !strings.Contains(got, "travis › On the 13th.") {
		t.Fatalf("got\n%s\nwant a label on each reply only, none on the blank chunk, card, or thought", got)
	}
}
