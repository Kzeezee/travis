package terminal

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/exp/teatest/v2"

	"github.com/Kzeezee/travis/internal/core"
)

var tabKey = tea.KeyPressMsg{Code: tea.KeyTab}

// fakeUndo serves turns oldest first, as the session log lists them, and records rewinds.
type fakeUndo struct {
	turns       []core.TurnSummary
	listErr     error
	report      core.RewindReport
	rewoundFrom core.ID
	rewoundMode core.RewindMode
}

func (u *fakeUndo) buildActions() UndoActions {
	return UndoActions{
		ListTurns: func(context.Context) ([]core.TurnSummary, error) { return u.turns, u.listErr },
		Rewind: func(_ context.Context, fromTurn core.ID, mode core.RewindMode) (core.RewindReport, error) {
			u.rewoundFrom, u.rewoundMode = fromTurn, mode
			return u.report, nil
		},
	}
}

func buildThreeTurns() []core.TurnSummary {
	at := func(hour, minute int) time.Time { return time.Date(2026, 10, 12, hour, minute, 0, 0, time.Local) }
	return []core.TurnSummary{
		{Turn: "t1", Prompt: "fix the typo in README", At: at(14, 15), ChangedPaths: []string{"README.md"}},
		{Turn: "t2", Prompt: "why does BuildPathSet fail on windows?", At: at(14, 21)},
		{Turn: "t3", Prompt: "rename the config loader\nand update callers", At: at(14, 32), ChangedPaths: []string{"config/load.go", "config/paths.go", "cmd/main.go"}},
	}
}

func buildUndoModel(undo *fakeUndo) chatModel {
	actions := ChatActions{RunTurn: func(context.Context, string) error { return nil }, Undo: undo.buildActions()}
	return buildChatModel(context.Background(), make(chan core.Event), nil, actions, Options{ModelName: "test-model"})
}

// submitUndo types /undo, presses enter and delivers the turn list, as the runtime would.
func submitUndo(t *testing.T, m chatModel) chatModel {
	t.Helper()
	m, cmd := pressKeys(m, append(typedKeys("/undo"), enterKey)...)
	if cmd == nil {
		t.Fatal("/undo should ask for the turn list")
	}
	m, _ = updateModel(m, cmd())
	return m
}

func TestUndoOpensThePickerNewestFirst(t *testing.T) {
	m := submitUndo(t, buildUndoModel(&fakeUndo{turns: buildThreeTurns()}))
	if m.undoPicker == nil {
		t.Fatal("want the picker open")
	}
	if got := m.undoPicker.turns[0].Turn; got != "t3" {
		t.Fatalf("first row = %s, want the newest turn", got)
	}
	view := renderPlainView(m)
	for _, want := range []string{"rename the config loader and update callers", "3 files", "1 file", "—", "◉ files + conversation", "rewinds 1 turn · puts back config/load.go, config/paths.go, +1"} {
		if !strings.Contains(view, want) {
			t.Fatalf("view\n%s\nlacks %q", view, want)
		}
	}
}

func TestUndoSaysWhenThereIsNothingToUndoOrItCannot(t *testing.T) {
	for name, tc := range map[string]struct {
		undo *fakeUndo
		want string
	}{
		"no turns":    {&fakeUndo{}, "nothing to undo yet"},
		"list failed": {&fakeUndo{listErr: errors.New("broker unreachable")}, "✗ undo: broker unreachable"},
	} {
		t.Run(name, func(t *testing.T) {
			m := submitUndo(t, buildUndoModel(tc.undo))
			if m.undoPicker != nil || !strings.Contains(renderPlainView(m), tc.want) {
				t.Fatalf("picker open = %v, view\n%s\nwant %q", m.undoPicker != nil, renderPlainView(m), tc.want)
			}
		})
	}
	m, cmd := pressKeys(buildTestModel(ChatActions{RunTurn: func(context.Context, string) error { return nil }}), append(typedKeys("/undo"), enterKey)...)
	if cmd != nil || !strings.Contains(renderPlainView(m), "undo is not available") {
		t.Fatalf("without undo actions: cmd = %v, view\n%s", cmd != nil, renderPlainView(m))
	}
}

func TestUndoPickerChoosesTurnAndModeAndRewinds(t *testing.T) {
	undo := &fakeUndo{turns: buildThreeTurns()}
	m := submitUndo(t, buildUndoModel(undo))

	m, _ = pressKeys(m, tea.KeyPressMsg{Code: tea.KeyDown}, tea.KeyPressMsg{Code: tea.KeyDown}, tea.KeyPressMsg{Code: tea.KeyDown}, tabKey, tabKey)
	if m.undoPicker.cursor != 2 || m.undoPicker.mode != core.RewindFilesOnly {
		t.Fatalf("cursor %d mode %s, want the last row in files-only mode", m.undoPicker.cursor, m.undoPicker.mode)
	}
	if view := renderPlainView(m); !strings.Contains(view, "puts back config/load.go, config/paths.go, +2 from 3 turns") {
		t.Fatalf("view\n%s\nwant the files-only preview for all three turns", view)
	}
	m, cmd := pressKeys(m, enterKey)
	if m.undoPicker != nil || cmd == nil {
		t.Fatal("enter should close the picker and rewind")
	}
	if _, isFinished := cmd().(rewindFinishedMsg); !isFinished || undo.rewoundFrom != "t1" || undo.rewoundMode != core.RewindFilesOnly {
		t.Fatalf("rewound from %q in %s mode, want t1 files-only", undo.rewoundFrom, undo.rewoundMode)
	}
}

func TestUndoPickerTakesEveryKeyAndEscCancels(t *testing.T) {
	undo := &fakeUndo{turns: buildThreeTurns()}
	m := submitUndo(t, buildUndoModel(undo))
	m, _ = pressKeys(m, typedKeys("hello")...)
	if m.input.Value() != "" {
		t.Fatalf("input = %q, want keys kept from it while the picker is open", m.input.Value())
	}
	m, cmd := pressKeys(m, escKey)
	if m.undoPicker != nil || cmd != nil || undo.rewoundFrom != "" {
		t.Fatal("esc should close the picker without rewinding")
	}
}

func TestUndoIsNotTakenWhileATurnRuns(t *testing.T) {
	m := buildUndoModel(&fakeUndo{turns: buildThreeTurns()})
	m, _ = pressKeys(m, append(typedKeys("first"), enterKey)...)
	m, cmd := pressKeys(m, append(typedKeys("/undo"), enterKey)...)
	if cmd != nil || m.undoPicker != nil {
		t.Fatal("/undo ran while a turn was running")
	}
}

// buildTranscriptWithTurns runs two turns through the transcript, each with its turn id, and has
// the second change a file.
func buildTranscriptWithTurns(t *testing.T) chatModel {
	t.Helper()
	m := buildUndoModel(&fakeUndo{})
	for i, prompt := range []string{"first ask", "second ask"} {
		turnID := core.ID([]string{"t1", "t2"}[i])
		var turn tea.Cmd
		m, turn = pressKeys(m, append(typedKeys(prompt), enterKey)...)
		events := []core.Event{
			core.UserMessageSubmitted{EventHeader: core.EventHeader{TurnID: turnID}},
			core.AssistantTextStreamed{Text: "answer to " + prompt},
		}
		if i == 1 {
			events = append(events, core.FileChanged{EventHeader: core.EventHeader{TurnID: turnID}, Path: "notes/today.md"})
		}
		m, _ = updateModel(m, eventBatchMsg{events: events})
		m, _ = updateModel(m, turn())
	}
	return m
}

func TestTurnShowsTheFilesItChanged(t *testing.T) {
	view := renderPlainView(buildTranscriptWithTurns(t))
	if !strings.Contains(view, "✎ changed notes/today.md · /undo to take back") {
		t.Fatalf("view\n%s\nwant the changed line under the second turn", view)
	}
}

func TestRewindingTheConversationHidesTurnsAndReturnsThePrompt(t *testing.T) {
	m := buildTranscriptWithTurns(t)
	report := core.RewindReport{Files: core.FileReverts{Restored: []string{"notes/today.md"}, Skipped: []core.SkippedFile{{Path: "b.md", Reason: "changed since the agent last wrote it"}}}}
	m, _ = updateModel(m, rewindFinishedMsg{fromTurn: "t2", mode: core.RewindFilesAndConversation, report: report})

	view := renderPlainView(m)
	if strings.Contains(view, "you › second ask") || strings.Contains(view, "answer to second ask") {
		t.Fatalf("view\n%s\nwant the second turn gone", view)
	}
	if !strings.Contains(view, "answer to first ask") || !strings.Contains(view, "↺ rewound the conversation · restored notes/today.md · left b.md (changed") {
		t.Fatalf("view\n%s\nwant the first turn kept and the rewind described", view)
	}
	if m.input.Value() != "second ask" {
		t.Fatalf("input = %q, want the rewound prompt back to edit", m.input.Value())
	}
}

func TestRewindingFilesOnlyKeepsTheConversation(t *testing.T) {
	m := buildTranscriptWithTurns(t)
	m, _ = updateModel(m, rewindFinishedMsg{fromTurn: "t2", mode: core.RewindFilesOnly, report: core.RewindReport{Files: core.FileReverts{Deleted: []string{"notes/today.md"}}}})

	view := renderPlainView(m)
	if !strings.Contains(view, "answer to second ask") || !strings.Contains(view, "↺ put files back; the conversation is kept · deleted notes/today.md") {
		t.Fatalf("view\n%s\nwant both turns kept and the file revert described", view)
	}
	if m.input.Value() != "" {
		t.Fatalf("input = %q, want it left alone", m.input.Value())
	}
}

// Regenerate with: go test ./internal/frontends/terminal -run TestUndoPickerGoldenFrames -update
func TestUndoPickerGoldenFrames(t *testing.T) {
	for _, width := range []int{50, 80} {
		t.Run(fmt.Sprintf("width_%d", width), func(t *testing.T) {
			m := submitUndo(t, buildUndoModel(&fakeUndo{turns: buildThreeTurns()}))
			m, _ = pressKeys(m, tea.WindowSizeMsg{Width: width, Height: 16}, tea.KeyPressMsg{Code: tea.KeyDown})
			teatest.RequireEqualOutput(t, []byte(renderPlainView(m)))
		})
	}
}

// A conversation rewind replaces the notices of the turns it hides: an older rewind's notice must
// not linger below the newer one.
func TestRewindReplacesTheNoticesOfTheTurnsItHides(t *testing.T) {
	m := buildTranscriptWithTurns(t)
	m, _ = updateModel(m, rewindFinishedMsg{fromTurn: "t2", mode: core.RewindFilesOnly, report: core.RewindReport{Files: core.FileReverts{Deleted: []string{"old.md"}}}})
	m, _ = updateModel(m, rewindFinishedMsg{fromTurn: "t1", mode: core.RewindFilesAndConversation})

	view := renderPlainView(m)
	if strings.Contains(view, "deleted old.md") || strings.Count(view, "↺") != 1 {
		t.Fatalf("view\n%s\nwant only the newest rewind's notice", view)
	}
}
