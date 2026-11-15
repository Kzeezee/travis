package terminal

import (
	"context"
	"slices"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

func TestOutputCommandShowsExactInvocationBeforeItsReport(t *testing.T) {
	actions := ChatActions{Inspect: InspectionActions{
		ReadContext: func(context.Context) (InspectionReport, error) {
			return InspectionReport{
				Summary:  "Context usage",
				Sections: []ReportSection{{Rows: []ReportRow{{Label: "Used", Value: "12k / 32k tokens"}}}},
			}, nil
		},
	}}
	m := buildTestModel(actions)

	m, cmd := pressKeys(m, append(typedKeys("/context"), enterKey)...)
	m = applyCmd(t, m, cmd)

	if got := m.transcript.notices; len(got) != 3 || got[0] != "> /context" ||
		got[1] != "Context usage" || got[2] != "Used: 12k / 32k tokens" {
		t.Fatalf("notices = %q, want invocation followed by report", got)
	}
}

func TestInteractiveCommandKeepsTranscriptVisibleAndEscRestoresEmptyPrompt(t *testing.T) {
	m := buildTestModel(ChatActions{})
	m.transcript.addNotice("earlier transcript output")
	m.refreshTranscript()
	before := slices.Clone(m.transcript.notices)

	m, _ = pressKeys(m, append(typedKeys("/theme"), enterKey)...)
	view := renderPlainView(m)
	if m.commandPanel == nil || !strings.Contains(view, "earlier transcript output") || strings.Contains(view, "> /theme") {
		t.Fatalf("view\n%s\nwant transcript and inline theme panel", view)
	}

	m, _ = pressKeys(m, append(typedKeys("must not leak"), escKey)...)
	if m.commandPanel != nil || m.input.Value() != "" || !slices.Equal(m.transcript.notices, before) {
		t.Fatalf("panel = %v, input = %q, notices = %q; want unchanged transcript and empty prompt",
			m.commandPanel != nil, m.input.Value(), m.transcript.notices)
	}
}

func TestThemeAndModelPanelsApplyTheSelectedOption(t *testing.T) {
	t.Run("theme", func(t *testing.T) {
		m := buildTestModel(ChatActions{})
		m, _ = pressKeys(m, append(typedKeys("/theme"), enterKey)...)
		m, _ = pressKeys(m, tea.KeyPressMsg{Code: tea.KeyDown}, enterKey)

		if m.commandPanel != nil || m.themePreference != themeNames[1] || m.input.Value() != "" {
			t.Fatalf("panel = %v, theme = %q, input = %q", m.commandPanel != nil, m.themePreference, m.input.Value())
		}
	})

	t.Run("model", func(t *testing.T) {
		selected := ""
		m := buildTestModel(ChatActions{SelectModel: func(name string) { selected = name }})
		m.modelNames = []string{"first", "second"}
		m, _ = pressKeys(m, append(typedKeys("/model"), enterKey)...)
		m, _ = pressKeys(m, tea.KeyPressMsg{Code: tea.KeyDown}, enterKey)

		if m.commandPanel != nil || selected != "second" || m.modelName != "second" || m.input.Value() != "" {
			t.Fatalf("panel = %v, selected = %q, model = %q, input = %q",
				m.commandPanel != nil, selected, m.modelName, m.input.Value())
		}
	})
}

func TestInlinePanelFitsAShortTerminal(t *testing.T) {
	m := buildTestModel(ChatActions{})
	m, _ = pressKeys(m, tea.WindowSizeMsg{Width: 24, Height: 8})
	m, _ = pressKeys(m, append(typedKeys("/help"), enterKey)...)

	if lines := strings.Count(renderPlainView(m), "\n") + 1; lines > 8 {
		t.Fatalf("rendered %d lines in an 8-line terminal", lines)
	}
}

func TestSettingsEscClosesFromSearchWithAnEmptyPrompt(t *testing.T) {
	actions := ChatActions{Settings: SettingsActions{
		Load: func(context.Context) (SettingsSnapshot, error) {
			return SettingsSnapshot{Rows: []SettingRow{{Key: "theme", Label: "Theme", Value: "dark"}}}, nil
		},
		Apply: func(context.Context, SettingChange) (SettingsSnapshot, error) { return SettingsSnapshot{}, nil },
	}}
	m := buildTestModel(actions)
	m.transcript.addNotice("earlier transcript output")
	before := slices.Clone(m.transcript.notices)
	m, cmd := pressKeys(m, append(typedKeys("/settings"), enterKey)...)
	m = applyCmd(t, m, cmd)
	m, _ = pressKeys(m, append(typedKeys("theme"), escKey)...)

	if m.settingsPanel != nil || m.input.Value() != "" || !slices.Equal(m.transcript.notices, before) {
		t.Fatalf("settings = %v, input = %q, notices = %q; want unchanged transcript and empty prompt",
			m.settingsPanel != nil, m.input.Value(), m.transcript.notices)
	}
}

func TestSettingsTextEditorMovesFocusToTheValueRow(t *testing.T) {
	actions := ChatActions{Settings: SettingsActions{
		Load: func(context.Context) (SettingsSnapshot, error) {
			return SettingsSnapshot{Rows: []SettingRow{{
				Key: "voice_key", Label: "Push-to-talk key", Value: "ctrl+t", Scope: "saved", Kind: SettingText,
			}}}, nil
		},
		Apply: func(context.Context, SettingChange) (SettingsSnapshot, error) { return SettingsSnapshot{}, nil },
	}}
	m := buildTestModel(actions)
	m, cmd := pressKeys(m, append(typedKeys("/settings"), enterKey)...)
	m = applyCmd(t, m, cmd)
	if m.View().Cursor == nil {
		t.Fatal("settings search did not expose a visible text cursor")
	}
	m, _ = pressKeys(m, enterKey)

	view := renderPlainView(m)
	editor := strings.Index(view, "▸ value › ctrl+t")
	row := strings.Index(view, "Push-to-talk key: ctrl+t")
	if !strings.Contains(view, "/settings · editing Push-to-talk key") || editor < 0 || row < 0 || editor >= row ||
		strings.Count(view, "▸") != 1 || !strings.Contains(view, "enter save · esc cancel") {
		t.Fatalf("view\n%s\nwant one highlighted value editor above the unselected setting row", view)
	}
	if m.View().Cursor == nil {
		t.Fatal("settings value editor did not expose a visible text cursor")
	}
	if got, want := m.View().Cursor.Y, strings.Count(view[:editor], "\n"); got != want {
		t.Fatalf("settings cursor row = %d, want editor row %d", got, want)
	}
	editorLine := strings.Split(view, "\n")[m.View().Cursor.Y]
	valueEnd := strings.Index(editorLine, "ctrl+t") + len("ctrl+t")
	if got, want := m.View().Cursor.X, ansi.StringWidth(editorLine[:valueEnd]); got != want {
		t.Fatalf("settings cursor column = %d, want value end %d", got, want)
	}
	m, _ = pressKeys(m, typedKeys("x")...)
	if got := m.settingsPanel.editor.Value(); got != "ctrl+tx" {
		t.Fatalf("editor value = %q, want typed text to reach the focused value editor", got)
	}
}

func TestMemoryEditSelectionOpensTheEditor(t *testing.T) {
	actions := ChatActions{Memory: MemoryActions{
		Read: func(context.Context) (MemoryDocument, error) {
			return MemoryDocument{Trusted: "remember this", Revision: "1", MaxBytes: 1024, MaxLines: 20}, nil
		},
		Change: func(context.Context, MemoryChange) (MemoryDocument, error) { return MemoryDocument{}, nil },
	}}
	m := buildTestModel(actions)
	m, _ = pressKeys(m, append(typedKeys("/memory"), enterKey)...)
	m, cmd := pressKeys(m, enterKey)
	if cmd == nil {
		t.Fatal("selecting Edit did not request saved memory")
	}
	if m.commandPanel == nil || !strings.Contains(renderPlainView(m), "loading saved memory") {
		t.Fatalf("view while loading\n%s\nwant immediate memory-loading feedback", renderPlainView(m))
	}
	m = applyCmd(t, m, cmd)

	if m.memoryEditor == nil || !strings.Contains(renderPlainView(m), "/memory edit") ||
		!strings.Contains(renderPlainView(m), "remember this") {
		t.Fatalf("editor = %v, view\n%s\nwant the memory editor with saved content", m.memoryEditor != nil, renderPlainView(m))
	}
	if m.View().Cursor == nil {
		t.Fatal("memory editor did not expose a visible text cursor")
	}
	memoryView := renderPlainView(m)
	content := strings.Index(memoryView, "  remember this")
	if content < 0 {
		t.Fatalf("memory editor view\n%s\nwant saved content", memoryView)
	}
	if got, want := m.View().Cursor.Y, strings.Count(memoryView[:content], "\n"); got != want {
		t.Fatalf("memory cursor row = %d, want editor row %d", got, want)
	}
	m, _ = pressKeys(m, typedKeys("!")...)
	if got := m.memoryEditor.textarea.Value(); got != "remember this!" {
		t.Fatalf("editor value = %q, want typed text to reach the focused editor", got)
	}

	empty, _ := buildTestModel(actions).openMemoryEditor(MemoryReplace, MemoryDocument{})
	if view := renderPlainView(empty); !strings.Contains(view, "Type trusted notes here…") {
		t.Fatalf("empty editor view\n%s\nwant an explanatory placeholder", view)
	}
}

func TestEscClosedMemoryLoaderStaysClosedWhenReadFinishes(t *testing.T) {
	actions := ChatActions{Memory: MemoryActions{
		Read:   func(context.Context) (MemoryDocument, error) { return MemoryDocument{}, nil },
		Change: func(context.Context, MemoryChange) (MemoryDocument, error) { return MemoryDocument{}, nil },
	}}
	m := buildTestModel(actions)
	m, _ = pressKeys(m, append(typedKeys("/memory"), enterKey)...)
	m, cmd := pressKeys(m, enterKey)
	m, _ = pressKeys(m, escKey)
	m = applyCmd(t, m, cmd)

	if m.commandPanel != nil || m.memoryEditor != nil || m.input.Value() != "" {
		t.Fatalf("panel = %v, editor = %v, input = %q; cancelled memory editor reopened",
			m.commandPanel != nil, m.memoryEditor != nil, m.input.Value())
	}
}

func TestPermissionsReportUsesAnInlinePanel(t *testing.T) {
	actions := ChatActions{Inspect: InspectionActions{
		ReadPermissions: func(context.Context) (InspectionReport, error) {
			return InspectionReport{
				Summary:  "Effective permissions",
				Sections: []ReportSection{{Rows: []ReportRow{{Label: "Profile", Value: "standard"}}}},
			}, nil
		},
	}}
	m := buildTestModel(actions)
	m.transcript.addNotice("earlier transcript output")
	m.refreshTranscript()
	before := slices.Clone(m.transcript.notices)
	m, cmd := pressKeys(m, append(typedKeys("/permissions"), enterKey)...)
	m = applyCmd(t, m, cmd)

	view := renderPlainView(m)
	if m.commandPanel == nil || !strings.Contains(view, "earlier transcript output") ||
		!strings.Contains(view, "Effective permissions") || !strings.Contains(view, "Profile: standard") {
		t.Fatalf("view\n%s\nwant transcript and inline permissions report", view)
	}
	m, _ = pressKeys(m, escKey)
	if m.commandPanel != nil || m.input.Value() != "" || !slices.Equal(m.transcript.notices, before) {
		t.Fatalf("panel = %v, input = %q, notices = %q; want unchanged transcript and empty prompt",
			m.commandPanel != nil, m.input.Value(), m.transcript.notices)
	}
}

func TestEscClosedLoadingPanelStaysClosedWhenItsResultArrives(t *testing.T) {
	actions := ChatActions{Settings: SettingsActions{
		Load:  func(context.Context) (SettingsSnapshot, error) { return SettingsSnapshot{}, nil },
		Apply: func(context.Context, SettingChange) (SettingsSnapshot, error) { return SettingsSnapshot{}, nil },
	}}
	m := buildTestModel(actions)
	m, cmd := pressKeys(m, append(typedKeys("/settings"), enterKey)...)
	m, _ = pressKeys(m, escKey)
	m = applyCmd(t, m, cmd)

	if m.commandPanel != nil || m.settingsPanel != nil {
		t.Fatal("a cancelled loading panel reopened when its result arrived")
	}
}
