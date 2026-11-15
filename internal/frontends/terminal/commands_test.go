package terminal

import (
	"context"
	"slices"
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
		{name: "exit alias", word: "exit", wantName: "exit", wantFound: true},
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

func TestSlashCommandInputIsRecordedOnlyWhenItProducesTranscriptOutput(t *testing.T) {
	tests := []struct {
		input string
		want  bool
	}{
		{input: "/help"},
		{input: "/settings"},
		{input: "/settings ignored"},
		{input: "/theme"},
		{input: "/model"},
		{input: "/provider"},
		{input: "/permissions"},
		{input: "/memory"},
		{input: "/memory edit"},
		{input: "/memory clear"},
		{input: "/undo"},
		{input: "/theme light", want: true},
		{input: "/model qwen", want: true},
		{input: "/provider local", want: true},
		{input: "/permissions revoke rule-1", want: true},
		{input: "/memory add remember this", want: true},
		{input: "/context", want: true},
		{input: "/doctor live", want: true},
	}
	for _, tt := range tests {
		name, args, _ := strings.Cut(strings.TrimPrefix(tt.input, "/"), " ")
		if got := shouldRecordSlashCommand(name, strings.TrimSpace(args)); got != tt.want {
			t.Errorf("%s: record = %v, want %v", tt.input, got, tt.want)
		}
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
		{prefix: "t", want: 2}, // tools, theme
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

	toolNames    []string
	skills       []SkillSummary
	voiceState   VoiceConfiguration
	voiceChanges []VoiceChange
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
		ConfigureVoice: func(change VoiceChange) (VoiceConfiguration, error) {
			r.voiceChanges = append(r.voiceChanges, change)
			if change.InputEnabled != nil {
				r.voiceState.IsInputEnabled = *change.InputEnabled
			}
			if change.OutputEnabled != nil {
				r.voiceState.IsOutputEnabled = *change.OutputEnabled
			}
			if change.PushToTalkKey != nil {
				r.voiceState.PushToTalkKey = *change.PushToTalkKey
			}
			return r.voiceState, nil
		},
	}
}

func TestSlashCommandsDispatch(t *testing.T) {
	t.Run("/exit quits without reaching the model", func(t *testing.T) {
		recorder := &recordingActions{}
		m := buildTestModel(recorder.buildChatActions())

		_, cmd := pressKeys(m, append(typedKeys("/exit"), enterKey)...)

		if !isQuitCmd(cmd) || len(recorder.prompts) != 0 {
			t.Fatalf("quit = %t, prompts = %v; want quit with no model dispatch", isQuitCmd(cmd), recorder.prompts)
		}
	})

	t.Run("/new resets history and the transcript", func(t *testing.T) {
		recorder := &recordingActions{}
		m := buildTestModel(recorder.buildChatActions())
		m = runTestTurn(t, m, "earlier question")

		m, _ = pressKeys(m, append(typedKeys("/new"), enterKey)...)

		if recorder.resets != 1 {
			t.Fatalf("got %d resets, want 1", recorder.resets)
		}
		view := renderPlainView(m)
		if strings.Contains(view, "earlier question") || !strings.Contains(view, "> /new") ||
			!strings.Contains(view, "new conversation") {
			t.Fatalf("got view\n%s\nwant earlier turns gone, the command marker, and a new conversation notice", view)
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

	t.Run("/model without a name opens a picker with the current model", func(t *testing.T) {
		recorder := &recordingActions{}
		m := buildTestModel(recorder.buildChatActions())

		m, _ = pressKeys(m, append(typedKeys("/model"), enterKey)...)

		if recorder.selectedModel != "" || m.commandPanel == nil || !strings.Contains(renderPlainView(m), "current: test-model") {
			t.Fatalf("got selected %q and view\n%s\nwant no switch and an open model panel", recorder.selectedModel, renderPlainView(m))
		}
	})

	t.Run("/voice reports and changes input output and the key", func(t *testing.T) {
		recorder := &recordingActions{voiceState: VoiceConfiguration{PushToTalkKey: defaultPushToTalkKey}}
		m := buildTestModel(recorder.buildChatActions())

		m, _ = pressKeys(m, append(typedKeys("/voice status"), enterKey)...)
		if view := renderPlainView(m); !strings.Contains(view, "Voice input: Disabled") ||
			!strings.Contains(view, "Voice output: Disabled") || !strings.Contains(view, "Push-to-talk key: ctrl+t") {
			t.Fatalf("got view\n%s\nwant the initial voice status", view)
		}

		var cmd tea.Cmd
		m, cmd = pressKeys(m, append(typedKeys("/voice input on"), enterKey)...)
		m, _ = updateModel(m, cmd())
		m, cmd = pressKeys(m, append(typedKeys("/voice output on"), enterKey)...)
		m, _ = updateModel(m, cmd())
		m, cmd = pressKeys(m, append(typedKeys("/voice key ctrl+r"), enterKey)...)
		m, _ = updateModel(m, cmd())

		if len(recorder.voiceChanges) != 3 || !m.isVoiceInputEnabled || !m.isVoiceOutputEnabled || m.pushToTalkKey != "ctrl+r" {
			t.Fatalf("got changes %d and state input=%v output=%v key=%q", len(recorder.voiceChanges),
				m.isVoiceInputEnabled, m.isVoiceOutputEnabled, m.pushToTalkKey)
		}
		if view := renderPlainView(m); !strings.Contains(view, "✓ Push-to-talk key: ctrl+r") {
			t.Fatalf("got view\n%s\nwant the saved key notice", view)
		}
	})

	t.Run("/voice rejects invalid syntax without changing configuration", func(t *testing.T) {
		recorder := &recordingActions{}
		m := buildTestModel(recorder.buildChatActions())

		m, cmd := pressKeys(m, append(typedKeys("/voice output maybe"), enterKey)...)

		if cmd != nil || len(recorder.voiceChanges) != 0 || !strings.Contains(renderPlainView(m), "usage: "+voiceCommandUsage) {
			t.Fatalf("got cmd=%v changes=%d view\n%s", cmd != nil, len(recorder.voiceChanges), renderPlainView(m))
		}
	})

	t.Run("unknown command never runs a turn", func(t *testing.T) {
		recorder := &recordingActions{}
		m := buildTestModel(recorder.buildChatActions())

		m, cmd := pressKeys(m, append(typedKeys("/foo bar"), enterKey)...)

		if len(recorder.prompts) != 0 || cmd != nil {
			t.Fatalf("got prompts %q, want none sent to the model", recorder.prompts)
		}
		if view := renderPlainView(m); !strings.Contains(view, "> /foo bar") ||
			!strings.Contains(view, "! Unknown command: /foo") ||
			!strings.Contains(view, "Run /help to see available commands.") {
			t.Fatalf("got view\n%s\nwant the submitted command and unknown command notice", view)
		}
	})

	t.Run("/tools lists the tool names live from the registry", func(t *testing.T) {
		recorder := &recordingActions{toolNames: []string{"fs.read", "fs.write"}}
		m := buildTestModel(recorder.buildChatActions())

		m, _ = pressKeys(m, append(typedKeys("/tools"), enterKey)...)

		if view := renderPlainView(m); !strings.Contains(view, "✓ fs.read: Available") ||
			!strings.Contains(view, "✓ fs.write: Available") {
			t.Fatalf("got view\n%s\nwant the tool list", view)
		}
	})

	t.Run("/skills lists each loaded skill with its tier and description", func(t *testing.T) {
		recorder := &recordingActions{skills: []SkillSummary{
			{Name: "notes", Tier: "user", Description: "search and summarize notes"},
		}}
		m := buildTestModel(recorder.buildChatActions())

		m, _ = pressKeys(m, append(typedKeys("/skills"), enterKey)...)

		if view := renderPlainView(m); !strings.Contains(view, "✓ notes: search and summarize notes") ||
			!strings.Contains(view, "Installed for your user account.") {
			t.Fatalf("got view\n%s\nwant the skill summary", view)
		}
	})

	t.Run("/memory opens the memory actions", func(t *testing.T) {
		recorder := &recordingActions{}
		actions := recorder.buildChatActions()
		actions.Memory = MemoryActions{
			Read:   func(context.Context) (MemoryDocument, error) { return MemoryDocument{}, nil },
			Change: func(context.Context, MemoryChange) (MemoryDocument, error) { return MemoryDocument{}, nil },
		}
		m := buildTestModel(actions)
		m.transcript.addNotice("earlier transcript output")
		before := slices.Clone(m.transcript.notices)

		m, _ = pressKeys(m, append(typedKeys("/memory"), enterKey)...)

		if view := renderPlainView(m); m.commandPanel == nil || !strings.Contains(view, "Edit your trusted notes") ||
			!strings.Contains(view, "Remove your trusted notes") || strings.Contains(view, "Show") {
			t.Fatalf("got view\n%s\nwant the memory action panel", view)
		}
		m, _ = pressKeys(m, escKey)
		if m.commandPanel != nil || m.input.Value() != "" || !slices.Equal(m.transcript.notices, before) {
			t.Fatalf("panel = %v, input = %q, notices = %q; want unchanged transcript and empty prompt",
				m.commandPanel != nil, m.input.Value(), m.transcript.notices)
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

		if view := renderPlainView(m); !strings.Contains(view, "Input tokens: 15") ||
			!strings.Contains(view, "Output tokens: 5") || !strings.Contains(view, "Total tokens: 20") {
			t.Fatalf("got view\n%s\nwant the summed usage", view)
		}
	})

	t.Run("/cost adds the session spend when the active profile is priced", func(t *testing.T) {
		recorder := &recordingActions{}
		m := buildChatModel(context.Background(), make(chan core.Event), nil, recorder.buildChatActions(), Options{ModelName: "test-model", IsPriced: true})
		m, _ = updateModel(m, eventBatchMsg{events: []core.Event{
			core.TurnCompleted{Usage: core.Usage{InputTokens: 10, OutputTokens: 3}, CostUSD: 0.10},
			core.TurnCompleted{Usage: core.Usage{InputTokens: 5, OutputTokens: 2}, CostUSD: 0.02},
		}})

		m, _ = pressKeys(m, append(typedKeys("/cost"), enterKey)...)

		if view := renderPlainView(m); !strings.Contains(view, "Input tokens: 15") ||
			!strings.Contains(view, "Output tokens: 5") || !strings.Contains(view, "Estimated cost: $0.12") {
			t.Fatalf("got view\n%s\nwant the summed usage and spend", view)
		}
	})

	t.Run("/cost shows no dollar figure when the active profile is unpriced", func(t *testing.T) {
		recorder := &recordingActions{}
		m := buildTestModel(recorder.buildChatActions())
		m, _ = updateModel(m, eventBatchMsg{events: []core.Event{
			core.TurnCompleted{Usage: core.Usage{InputTokens: 10, OutputTokens: 3}},
		}})

		m, _ = pressKeys(m, append(typedKeys("/cost"), enterKey)...)

		if view := renderPlainView(m); strings.Contains(view, "$") {
			t.Fatalf("got view\n%s\nwant no dollar figure", view)
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
