package terminal

import (
	"context"
	"strings"
	"testing"

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
		{prefix: "m", want: 1},
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
}

func (r *recordingActions) buildChatActions() ChatActions {
	return ChatActions{
		RunTurn: func(_ context.Context, prompt string) error {
			r.prompts = append(r.prompts, prompt)
			return nil
		},
		ResetHistory: func() { r.resets++ },
		SelectModel:  func(name string) { r.selectedModel = name },
		ToolNames:    []string{"fs.read", "fs.write"},
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

	t.Run("/tools lists the tool names", func(t *testing.T) {
		recorder := &recordingActions{}
		m := buildTestModel(recorder.buildChatActions())

		m, _ = pressKeys(m, append(typedKeys("/tools"), enterKey)...)

		if view := renderPlainView(m); !strings.Contains(view, "tools: fs.read, fs.write") {
			t.Fatalf("got view\n%s\nwant the tool list", view)
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
