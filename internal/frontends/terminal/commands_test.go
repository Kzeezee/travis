package terminal

import (
	"context"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/Kzeezee/travis/internal/core"
)

func TestSlashCommandLookup(t *testing.T) {
	tests := []struct {
		name      string
		word      string
		wantName  string
		wantFound bool
	}{
		{name: "by name", word: "model", wantName: "model", wantFound: true},
		{name: "former alias is unknown", word: "clear"},
		{name: "unknown", word: "foo"},
		{name: "empty word", word: ""},
		{name: "undo", word: "undo", wantName: "undo", wantFound: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			command, isFound := findSlashCommand(buildSlashCommands(), tt.word)
			if isFound != tt.wantFound || command.name != tt.wantName {
				t.Fatalf("got %q (found %v), want %q (found %v)", command.name, isFound, tt.wantName, tt.wantFound)
			}
		})
	}
}

func TestFilterSlashCommandsByPrefix(t *testing.T) {
	commands := buildSlashCommands()
	tests := []struct {
		prefix string
		want   int
	}{
		{prefix: "", want: len(commands)},
		{prefix: "m", want: 3}, // model, mcp, memory
		{prefix: "t", want: 1},
		{prefix: "x", want: 0},
	}

	for _, tt := range tests {
		if got := filterSlashCommands(commands, tt.prefix); len(got) != tt.want {
			t.Fatalf("prefix %q: got %d commands, want %d", tt.prefix, len(got), tt.want)
		}
	}
}

// recordingActions is a fake session that records which operations the chat asked for.
type recordingActions struct {
	prompts       []string
	resets        int
	selectedModel string

	toolNames []string
	skills    []SkillSummary
	memory    string
	memoryErr error
}

func (r *recordingActions) buildChatActions() ChatActions {
	return ChatActions{
		RunTurn: func(_ context.Context, prompt string) error {
			r.prompts = append(r.prompts, prompt)
			return nil
		},
		ResetHistory: func() { r.resets++ },
		SelectModel:  func(name string) { r.selectedModel = name },
		ListTools:    func() []string { return r.toolNames },
		ListSkills:   func() []SkillSummary { return r.skills },
		ReadMemory:   func(context.Context) (string, error) { return r.memory, r.memoryErr },
	}
}

func TestSlashCommandsDispatch(t *testing.T) {
	t.Run("/new resets history and the transcript", func(t *testing.T) {
		recorder := &recordingActions{}
		m := buildTestModel(recorder.buildChatActions())
		m = runTestTurn(t, m, "earlier question")

		m, _ = pressKeys(m, append(typedKeys("/new"), enterKey)...)

		if recorder.resets != 1 {
			t.Fatalf("got %d resets, want 1", recorder.resets)
		}
		view := renderPlainView(m)
		if strings.Contains(view, "earlier question") || !strings.Contains(view, "new conversation") {
			t.Fatalf("got view\n%s\nwant earlier turns gone and a new conversation notice", view)
		}
	})

	t.Run("/model selects the model and updates the status bar", func(t *testing.T) {
		recorder := &recordingActions{}
		m := buildTestModel(recorder.buildChatActions())

		m, _ = pressKeys(m, append(typedKeys("/model gpt-x"), enterKey)...)

		if recorder.selectedModel != "gpt-x" {
			t.Fatalf("got model %q, want gpt-x", recorder.selectedModel)
		}
		if view := renderPlainView(m); !strings.Contains(view, " gpt-x · idle") {
			t.Fatalf("got view\n%s\nwant the status bar to show gpt-x", view)
		}
	})

	t.Run("/model without a name reports the current model", func(t *testing.T) {
		recorder := &recordingActions{}
		m := buildTestModel(recorder.buildChatActions())

		m, _ = pressKeys(m, append(typedKeys("/model"), enterKey)...)

		if recorder.selectedModel != "" || !strings.Contains(renderPlainView(m), "model: test-model") {
			t.Fatalf("got selected %q and view\n%s\nwant no switch and the current model shown", recorder.selectedModel, renderPlainView(m))
		}
	})

	t.Run("unknown command never runs a turn", func(t *testing.T) {
		recorder := &recordingActions{}
		m := buildTestModel(recorder.buildChatActions())

		m, cmd := pressKeys(m, append(typedKeys("/foo bar"), enterKey)...)

		if len(recorder.prompts) != 0 || cmd != nil {
			t.Fatalf("got prompts %q, want none sent to the model", recorder.prompts)
		}
		if view := renderPlainView(m); !strings.Contains(view, "unknown command /foo — try /help") {
			t.Fatalf("got view\n%s\nwant the unknown command notice", view)
		}
	})

	t.Run("/tools lists the tool names live from the registry", func(t *testing.T) {
		recorder := &recordingActions{toolNames: []string{"fs.read", "fs.write"}}
		m := buildTestModel(recorder.buildChatActions())

		m, _ = pressKeys(m, append(typedKeys("/tools"), enterKey)...)

		if view := renderPlainView(m); !strings.Contains(view, "tools: fs.read, fs.write") {
			t.Fatalf("got view\n%s\nwant the tool list", view)
		}
	})

	t.Run("/skills lists each loaded skill with its tier and description", func(t *testing.T) {
		recorder := &recordingActions{skills: []SkillSummary{
			{Name: "notes", Tier: "user", Description: "search and summarize notes"},
		}}
		m := buildTestModel(recorder.buildChatActions())

		m, _ = pressKeys(m, append(typedKeys("/skills"), enterKey)...)

		if view := renderPlainView(m); !strings.Contains(view, "notes [user]: search and summarize notes") {
			t.Fatalf("got view\n%s\nwant the skill summary", view)
		}
	})

	t.Run("/memory shows the current memory content", func(t *testing.T) {
		recorder := &recordingActions{memory: "# Memory\n\nremember the deadline"}
		m := buildTestModel(recorder.buildChatActions())

		m, _ = pressKeys(m, append(typedKeys("/memory"), enterKey)...)

		if view := renderPlainView(m); !strings.Contains(view, "remember the deadline") {
			t.Fatalf("got view\n%s\nwant the memory content", view)
		}
	})

	t.Run("/cost totals the session usage", func(t *testing.T) {
		recorder := &recordingActions{}
		m := buildTestModel(recorder.buildChatActions())
		m, _ = updateModel(m, eventBatchMsg{events: []core.Event{
			core.TurnCompleted{Usage: core.Usage{InputTokens: 10, OutputTokens: 3}},
			core.TurnCompleted{Usage: core.Usage{InputTokens: 5, OutputTokens: 2}},
		}})

		m, _ = pressKeys(m, append(typedKeys("/cost"), enterKey)...)

		if view := renderPlainView(m); !strings.Contains(view, "tokens: in 15 · out 5") {
			t.Fatalf("got view\n%s\nwant the summed usage", view)
		}
	})
}

func runTestTurn(t *testing.T, m chatModel, prompt string) chatModel {
	t.Helper()
	m, turn := pressKeys(m, append(typedKeys(prompt), enterKey)...)
	m, _ = updateModel(m, turn())
	return m
}

func TestPaletteListsAndCompletesCommands(t *testing.T) {
	m := buildTestModel(ChatActions{})

	m, _ = pressKeys(m, typedKeys("/")...)
	if rows := m.listPaletteRows(); len(rows) != min(len(m.commands), maxPaletteRows) {
		t.Fatalf("got %d rows for /, want min(every command, the palette cap) = %d", len(rows), min(len(m.commands), maxPaletteRows))
	}
	m, _ = pressKeys(m, typedKeys("mo")...) // "m" alone now also matches /memory
	if rows := m.listPaletteRows(); len(rows) != 1 || rows[0].completion != "/model " {
		t.Fatalf("got rows %v for /mo, want only /model", rows)
	}
	if view := renderPlainView(m); !strings.Contains(view, "› /model [name]") {
		t.Fatalf("want /model shown selected, got:\n%s", view)
	}

	m, _ = pressKeys(m, tea.KeyPressMsg{Code: tea.KeyTab})
	if m.input.Value() != "/model " {
		t.Fatalf("got input %q, want tab to complete /model", m.input.Value())
	}
	if len(m.listPaletteRows()) != 0 {
		t.Fatal("the palette should close once arguments start")
	}
}

func TestPaletteKeysMoveSelectionAndEscClears(t *testing.T) {
	m := buildTestModel(ChatActions{})
	m, _ = pressKeys(m, typedKeys("/")...)
	fullHeight := m.viewport.Height()

	m, _ = pressKeys(m, tea.KeyPressMsg{Code: tea.KeyUp})
	if m.paletteIndex != 0 {
		t.Fatalf("got index %d, want up clamped at 0", m.paletteIndex)
	}
	for range len(m.commands) + 2 {
		m, _ = pressKeys(m, tea.KeyPressMsg{Code: tea.KeyDown})
	}
	rows := m.listPaletteRows()
	if want := len(rows) - 1; m.paletteIndex != want {
		t.Fatalf("got index %d, want down clamped at %d", m.paletteIndex, want)
	}
	m, _ = pressKeys(m, tea.KeyPressMsg{Code: tea.KeyTab})
	if m.input.Value() != rows[len(rows)-1].completion {
		t.Fatalf("got input %q, want the last visible command completed", m.input.Value())
	}

	m.input.Reset()
	m, _ = pressKeys(m, typedKeys("/")...)
	m, _ = pressKeys(m, escKey)
	if m.input.Value() != "" || m.viewport.Height() <= fullHeight {
		t.Fatal("esc should clear the input and give the rows back to the transcript")
	}
}

func TestPaletteEnterRunsTheSelectedCommand(t *testing.T) {
	recorder := &recordingActions{}
	m := buildTestModel(recorder.buildChatActions())
	m, _ = pressKeys(m, typedKeys("/n")...)
	m, _ = pressKeys(m, enterKey)
	if recorder.resets != 1 || len(recorder.prompts) != 0 {
		t.Fatalf("got %d resets and prompts %v, want /new run and nothing sent to the model", recorder.resets, recorder.prompts)
	}
	if m.input.Value() != "" {
		t.Fatalf("got input %q, want it cleared", m.input.Value())
	}
}

func TestPaletteSuggestsModelsForTheModelArgument(t *testing.T) {
	recorder := &recordingActions{}
	actions := recorder.buildChatActions()
	actions.ListModels = func(context.Context) ([]string, error) { return []string{"claude-x", "gpt-4o", "GPT-4o-mini"}, nil }
	m := buildTestModel(actions)

	m, _ = updateModel(m, listModels(m.ctx, actions.ListModels)())
	m, _ = pressKeys(m, typedKeys("/model g")...)
	rows := m.listPaletteRows()
	if len(rows) != 2 || rows[0].completion != "/model gpt-4o" || rows[1].completion != "/model GPT-4o-mini" {
		t.Fatalf("got rows %v for /model g, want the two case-insensitive g matches", rows)
	}

	m, _ = pressKeys(m, tea.KeyPressMsg{Code: tea.KeyTab})
	if m.input.Value() != "/model gpt-4o" {
		t.Fatalf("got input %q, want tab to complete the selected model", m.input.Value())
	}
	m, _ = pressKeys(m, enterKey)
	if recorder.selectedModel != "gpt-4o" || m.modelName != "gpt-4o" {
		t.Fatalf("got selected %q, want enter to switch to gpt-4o", recorder.selectedModel)
	}
}
