package terminal

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/colorprofile"
	"github.com/charmbracelet/x/ansi"

	"github.com/Kzeezee/travis/internal/core"
)

func TestRunChatRefusesNonTerminalIO(t *testing.T) {
	runTurn := func(context.Context, string) error {
		t.Fatal("a refused chat must not run a turn")
		return nil
	}
	opts := Options{Input: strings.NewReader("hello\r"), Output: &strings.Builder{}}

	if err := RunChat(context.Background(), nil, nil, ChatActions{RunTurn: runTurn}, opts); !errors.Is(err, ErrNotInteractive) {
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
			m := buildTestModel(ChatActions{RunTurn: func(_ context.Context, prompt string) error {
				gotPrompt = prompt
				return tt.turnErr
			}})

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
	m := buildChatModel(context.Background(), make(chan core.Event), nil, ChatActions{},
		Options{ModelName: "test-model", StartupWarnings: []string{"config: unknown key brain.max_stepz"}})

	if view := renderPlainView(m); !strings.Contains(view, "⚠ config: unknown key brain.max_stepz") {
		t.Fatalf("got view\n%s\nwant the startup warning", view)
	}
}

func TestQuestionIsShownAndTheTypedAnswerIsReturned(t *testing.T) {
	answers := make(chan string, 1)
	m := buildTestModel(ChatActions{})
	m.cancelTurn = func() {}
	m, _ = updateModel(m, questionRequestedMsg{request: core.QuestionRequest{
		Text: "Which day?", Respond: func(answer string) { answers <- answer },
	}})
	if view := renderPlainView(m); !strings.Contains(view, "Which day?") {
		t.Fatalf("question missing from view:\n%s", view)
	}
	m, _ = pressKeys(m, append(typedKeys(" Tuesday "), enterKey)...)
	select {
	case answer := <-answers:
		if answer != "Tuesday" {
			t.Fatalf("answer = %q", answer)
		}
	default:
		t.Fatal("question was not answered")
	}
	if m.pendingQuestion != nil {
		t.Fatal("answered question remains pending")
	}
}

func TestBrokerUnavailableReplacesTheStatusBarWithAFailureBar(t *testing.T) {
	m := buildChatModel(context.Background(), make(chan core.Event), nil, ChatActions{}, Options{ModelName: "test-model"})
	updated, _ := m.Update(eventBatchMsg{events: []core.Event{core.BrokerUnavailable{Reason: "broker unreachable: EOF"}}})

	view := renderPlainView(updated.(chatModel))
	if !strings.Contains(view, "security broker unreachable") || strings.Contains(view, "last turn:") {
		t.Fatalf("got view\n%s\nwant the failure bar in place of the status bar", view)
	}
}

func TestStatusBarShowsTheMCPSandboxTier(t *testing.T) {
	cases := map[string]struct {
		tiers []string
		want  string
	}{
		"no mcp server":                   {nil, ""},
		"sandboxed server":                {[]string{"seatbelt"}, "sandbox: seatbelt"},
		"unconfined server":               {[]string{"none"}, "⚠ sandbox: none"},
		"unconfined then sandboxed stays": {[]string{"none", "landlock"}, "⚠ sandbox: none"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			m := buildChatModel(context.Background(), make(chan core.Event), nil, ChatActions{}, Options{ModelName: "test-model"})
			var events []core.Event
			for _, tier := range c.tiers {
				events = append(events, core.MCPServerStarted{Server: "notes", Tier: tier})
			}
			updated, _ := m.Update(eventBatchMsg{events: events})
			view := renderPlainView(updated.(chatModel))
			if c.want == "" && strings.Contains(view, "sandbox:") || c.want != "" && !strings.Contains(view, c.want) {
				t.Fatalf("got view\n%s\nwant status bar to show %q", view, c.want)
			}
		})
	}
}

func TestCancelKeysInterruptTheRunningTurn(t *testing.T) {
	for _, cancelKey := range []tea.KeyPressMsg{escKey, ctrlKey('c')} {
		t.Run(cancelKey.String(), func(t *testing.T) {
			m := buildTestModel(ChatActions{RunTurn: func(ctx context.Context, _ string) error {
				<-ctx.Done()
				return ctx.Err()
			}})
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
			m := buildTestModel(ChatActions{RunTurn: func(context.Context, string) error {
				t.Fatal("quitting must not run a turn")
				return nil
			}})
			_, cmd := pressKeys(m, tt.msgs...)
			if !isQuitCmd(cmd) {
				t.Fatal("want the program to quit")
			}
		})
	}
}

func TestRunningTurnDefersEnterAndInputEnd(t *testing.T) {
	m := buildTestModel(ChatActions{RunTurn: func(context.Context, string) error { return nil }})
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
	m := buildTestModel(ChatActions{RunTurn: func(context.Context, string) error { return nil }})
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

func buildTestModel(actions ChatActions) chatModel {
	return buildChatModel(context.Background(), make(chan core.Event), nil, actions, Options{ModelName: "test-model"})
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

	got := ansi.Strip(renderTurn(turn, 80, &replyRenderer{}))
	if strings.Count(got, "travis ›") != 2 || !strings.Contains(got, "travis › Let me look.") || !strings.Contains(got, "travis › On the 13th.") {
		t.Fatalf("got\n%s\nwant a label on each reply only, none on the blank chunk, card, or thought", got)
	}
}

func TestPaletteTypedDuringATurnOpensWhenItFinishes(t *testing.T) {
	m := buildTestModel(ChatActions{RunTurn: func(context.Context, string) error { return nil }})
	idleHeight := m.viewport.Height()
	m, _ = pressKeys(m, typedKeys("hi")...)
	m, turn := pressKeys(m, enterKey)
	m, _ = pressKeys(m, typedKeys("/q")...) // "q" still matches exactly one command (quit)
	if m.viewport.Height() != idleHeight {
		t.Fatal("the palette must stay closed while a turn runs")
	}
	m, _ = updateModel(m, turn())
	if m.viewport.Height() != idleHeight-1 {
		t.Fatalf("got viewport height %d, want %d to make room for the palette", m.viewport.Height(), idleHeight-1)
	}
}

// Bracketed paste arrives as one tea.PasteMsg rather than a run of key presses, so the input
// never sees it unless Update forwards it (the paste was dropped entirely before this).
func TestPasteReachesThePrompt(t *testing.T) {
	m := buildTestModel(ChatActions{})

	next, _ := m.Update(tea.PasteMsg{Content: "git status --short"})

	if got := next.(chatModel).input.Value(); got != "git status --short" {
		t.Fatalf("prompt = %q, want the pasted text", got)
	}
}

// Multi-line paste stays in the prompt as one value: a newline inside a paste is content, not
// the Enter that submits a turn.
func TestPasteDoesNotSubmitTheTurn(t *testing.T) {
	submitted := make(chan string, 1)
	m := buildTestModel(ChatActions{RunTurn: func(_ context.Context, prompt string) error {
		submitted <- prompt
		return nil
	}})

	next, _ := m.Update(tea.PasteMsg{Content: "first\nsecond"})

	if len(submitted) != 0 {
		t.Fatalf("a paste submitted %q", <-submitted)
	}
	if got := next.(chatModel).input.Value(); !strings.Contains(got, "first") {
		t.Fatalf("prompt = %q, want the pasted text", got)
	}
}

// A paste must not slip into the prompt behind a modal: an approval is keyboard-only (D-10),
// and text the user cannot see is text they cannot choose to send.
func TestPasteIsIgnoredWhileAModalIsOpen(t *testing.T) {
	m, _ := buildPendingApproval(t)

	next, cmd := m.Update(tea.PasteMsg{Content: "y"})

	if got := next.(chatModel).input.Value(); got != "" {
		t.Fatalf("prompt = %q, want it untouched while the modal is open", got)
	}
	if cmd != nil {
		t.Fatal("a paste answered the approval modal")
	}
	if next.(chatModel).pendingApproval == nil {
		t.Fatal("the modal was dismissed by a paste")
	}
}

func TestRenderReplyShowsMarkdownWithoutRawMarkers(t *testing.T) {
	got := ansi.Strip((&replyRenderer{}).renderReply("A **bold** claim:\n\n- one\n- two\n\n```\ncode here\n```", 60))

	for _, raw := range []string{"**", "```", "- one"} {
		if strings.Contains(got, raw) {
			t.Fatalf("got\n%s\nwant no raw %q marker", got, raw)
		}
	}
	for _, shown := range []string{"travis › A bold claim:", "• one", "• two", "code here"} {
		if !strings.Contains(got, shown) {
			t.Fatalf("got\n%s\nwant %q", got, shown)
		}
	}
}

func TestRenderReplyKeepsTextOfHalfFinishedMarkdown(t *testing.T) {
	// The 33ms coalescer can emit any prefix of a reply, so every prefix must render.
	reply := "Try:\n\n```go\nfmt.Println(\"hi\")\n"
	for end := range len(reply) + 1 {
		(&replyRenderer{}).renderReply(reply[:end], 60)
	}

	got := ansi.Strip((&replyRenderer{}).renderReply(reply, 60))
	if !strings.Contains(got, `fmt.Println("hi")`) || !strings.Contains(got, "travis › Try:") {
		t.Fatalf("got\n%s\nwant the text of the unclosed fence", got)
	}
}

func TestLowColourTerminalKeepsMarkdownPlain(t *testing.T) {
	m := buildTestModel(ChatActions{})
	m.transcript.startTurn("q")
	m, _ = updateModel(m, tea.ColorProfileMsg{Profile: colorprofile.ANSI})
	m, _ = updateModel(m, eventBatchMsg{events: []core.Event{
		core.UserMessageSubmitted{},
		core.AssistantTextStreamed{Text: "A **bold** claim"},
	}})

	if got := renderPlainView(m); !strings.Contains(got, "travis › A **bold** claim") {
		t.Fatalf("got\n%s\nwant markdown left untouched", got)
	}

	m, _ = updateModel(m, tea.ColorProfileMsg{Profile: colorprofile.ANSI256})
	if got := renderPlainView(m); !strings.Contains(got, "travis › A bold claim") {
		t.Fatalf("got\n%s\nwant markdown rendered once 256 colours are reported", got)
	}
}
