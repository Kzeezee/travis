package terminal

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
)

type SettingKind string

const (
	SettingToggle SettingKind = "toggle"
	SettingChoice SettingKind = "choice"
	SettingText   SettingKind = "text"
)

// SettingRow is one curated setting; Key is an enum owned by the composition adapter, not a TOML path.
type SettingRow struct {
	Key, Label, Value, Scope string
	Kind                     SettingKind
	Choices                  []string
}

type SettingsSnapshot struct {
	Rows           []SettingRow
	StartDictation func(context.Context) (func() (string, error), error)
}

type SettingChange struct {
	Key, Value string
}

type SettingsActions struct {
	Load  func(context.Context) (SettingsSnapshot, error)
	Apply func(context.Context, SettingChange) (SettingsSnapshot, error)
}

type settingsLoadedMsg struct {
	snapshot SettingsSnapshot
	err      error
}

type settingAppliedMsg struct {
	change   SettingChange
	snapshot SettingsSnapshot
	err      error
}

type settingsPanel struct {
	snapshot  SettingsSnapshot
	search    textinput.Model
	editor    textinput.Model
	cursor    int
	isEditing bool
	pending   bool
	notice    string
}

func openSettings(m chatModel, args string) (chatModel, tea.Cmd) {
	if strings.TrimSpace(args) != "" {
		m.transcript.addNotice("usage: /settings")
		return m, nil
	}
	if m.cancelTurn != nil {
		m.transcript.addNotice("! Settings cannot change while a task is running.")
		return m, nil
	}
	if m.actions.Settings.Load == nil || m.actions.Settings.Apply == nil {
		return m.openCommandPanel(commandPanel{
			kind: commandPanelSettings, title: "settings", lines: []string{"settings are unavailable"},
		}), nil
	}
	load, ctx := m.actions.Settings.Load, m.ctx
	m = m.openCommandPanel(commandPanel{
		kind: commandPanelSettings, title: "settings", lines: []string{"loading settings…"},
	})
	return m, func() tea.Msg {
		snapshot, err := load(ctx)
		return settingsLoadedMsg{snapshot: snapshot, err: err}
	}
}

func (m chatModel) finishSettingsLoad(msg settingsLoadedMsg) (tea.Model, tea.Cmd) {
	if m.commandPanel == nil || m.commandPanel.kind != commandPanelSettings {
		return m, nil
	}
	if msg.err != nil {
		m = m.openCommandPanel(commandPanel{
			kind: commandPanelSettings, title: "settings", lines: []string{"✗ " + cutFirstLine(msg.err.Error())},
		})
		return m, nil
	}
	search := textinput.New()
	search.Prompt = " search › "
	search.Placeholder = "type to filter"
	search.SetVirtualCursor(false)
	editor := textinput.New()
	editor.Prompt = " value › "
	editor.SetVirtualCursor(false)
	focus := search.Focus()
	m.commandPanel = nil
	m.settingsPanel = &settingsPanel{snapshot: msg.snapshot, search: search, editor: editor}
	m.fitViewport()
	m, mute := m.muteDictationForModal()
	return m, tea.Batch(focus, mute)
}

func (p settingsPanel) filteredRows() []SettingRow {
	query := strings.ToLower(strings.TrimSpace(p.search.Value()))
	if query == "" {
		return p.snapshot.Rows
	}
	var rows []SettingRow
	for _, row := range p.snapshot.Rows {
		if strings.Contains(strings.ToLower(row.Label+" "+row.Value+" "+row.Scope), query) {
			rows = append(rows, row)
		}
	}
	return rows
}

func (m chatModel) handleSettingsKey(key tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	panel := *m.settingsPanel
	if key.String() == "esc" || key.String() == "ctrl+c" {
		m.settingsPanel = nil
		m.input.Reset()
		m.fitViewport()
		return m, nil
	}
	if panel.pending {
		return m, nil
	}
	if panel.isEditing {
		switch key.String() {
		case "enter":
			rows := panel.filteredRows()
			if len(rows) == 0 {
				return m, nil
			}
			return m.applySetting(panel, SettingChange{Key: rows[min(panel.cursor, len(rows)-1)].Key, Value: panel.editor.Value()})
		}
		var cmd tea.Cmd
		panel.editor, cmd = panel.editor.Update(key)
		m.settingsPanel = &panel
		return m, cmd
	}
	rows := panel.filteredRows()
	switch key.String() {
	case "up":
		panel.cursor = max(panel.cursor-1, 0)
	case "down":
		panel.cursor = min(panel.cursor+1, max(len(rows)-1, 0))
	case "enter", "left", "right", "space":
		if len(rows) == 0 {
			return m, nil
		}
		row := rows[min(panel.cursor, len(rows)-1)]
		switch row.Kind {
		case SettingToggle:
			value := "true"
			if row.Value == "true" || row.Value == "on" {
				value = "false"
			}
			return m.applySetting(panel, SettingChange{Key: row.Key, Value: value})
		case SettingChoice:
			if len(row.Choices) == 0 {
				return m, nil
			}
			step := 1
			if key.String() == "left" {
				step = -1
			}
			index := slices.Index(row.Choices, row.Value)
			value := row.Choices[(max(index, 0)+step+len(row.Choices))%len(row.Choices)]
			return m.applySetting(panel, SettingChange{Key: row.Key, Value: value})
		case SettingText:
			panel.isEditing = true
			panel.editor.SetValue(row.Value)
			panel.editor.CursorEnd()
			panel.search.Blur()
			focus := panel.editor.Focus()
			m.settingsPanel = &panel
			return m, focus
		}
	default:
		var cmd tea.Cmd
		panel.search, cmd = panel.search.Update(key)
		panel.cursor = 0
		m.settingsPanel = &panel
		return m, cmd
	}
	m.settingsPanel = &panel
	return m, nil
}

func (m chatModel) applySetting(panel settingsPanel, change SettingChange) (tea.Model, tea.Cmd) {
	panel.pending = true
	panel.notice = "saving " + change.Key + "…"
	panel.isEditing = false
	m.settingsPanel = &panel
	apply, ctx := m.actions.Settings.Apply, m.ctx
	return m, func() tea.Msg {
		snapshot, err := apply(ctx, change)
		return settingAppliedMsg{change: change, snapshot: snapshot, err: err}
	}
}

func (m chatModel) finishSettingApply(msg settingAppliedMsg) (tea.Model, tea.Cmd) {
	if m.settingsPanel == nil {
		return m, nil
	}
	panel := *m.settingsPanel
	panel.pending = false
	if msg.err != nil {
		panel.notice = "✗ " + cutFirstLine(msg.err.Error())
	} else {
		panel.snapshot = msg.snapshot
		panel.notice = msg.change.Key + " → " + msg.change.Value
		m.applySettingsToRuntime(msg.snapshot)
	}
	focus := panel.search.Focus()
	m.settingsPanel = &panel
	return m, focus
}

func (m *chatModel) applySettingsToRuntime(snapshot SettingsSnapshot) {
	m.actions.StartDictation = snapshot.StartDictation
	for _, row := range snapshot.Rows {
		switch row.Key {
		case "theme":
			m.themePreference = row.Value
			resolved := row.Value
			if resolved == themeAuto {
				resolved = themeDark
				if !m.isDarkBackground {
					resolved = themeLight
				}
			}
			m.applyTheme(buildTerminalTheme(resolved))
		case "reduced_motion":
			m.isReducedMotion = row.Value == "true"
		case "voice_input":
			m.isVoiceInputEnabled = row.Value == "on"
		case "voice_output":
			m.isVoiceOutputEnabled = row.Value == "on"
		case "voice_key":
			m.pushToTalkKey = row.Value
		case "max_turn_cost":
			_, _ = fmt.Sscan(row.Value, &m.maxTurnCostUSD)
		case "max_daily_cost":
			_, _ = fmt.Sscan(row.Value, &m.maxDailyCostUSD)
		case "model":
			m.modelName = row.Value
		}
	}
}

func renderSettingsPanel(panel settingsPanel, width, height int, theme terminalTheme) string {
	legend := "↑↓ choose · enter edit/toggle · ←→ cycle · esc close"
	title := "/settings · saved preferences"
	control := panel.search.View()
	if panel.isEditing {
		if rows := panel.filteredRows(); len(rows) > 0 {
			title = "/settings · editing " + rows[min(panel.cursor, len(rows)-1)].Label
		}
		control = theme.selectedRow.Render("▸") + panel.editor.View()
		legend = "type a new value · enter save · esc cancel"
	}
	if width < 60 {
		legend = "↑↓ choose · enter edit · ←→ change · esc"
		if panel.isEditing {
			legend = "type value · enter save · esc cancel"
		}
	}
	if width < 40 {
		legend = "↑↓ · enter · esc"
		if panel.isEditing {
			legend = "enter save · esc cancel"
		}
	}
	lines := []string{
		truncateWithMargin(theme.assistantLabel.Render(title), width),
		control,
	}
	rows := panel.filteredRows()
	footerRows := 1
	if panel.notice != "" {
		footerRows++
	}
	available := max(height-len(lines)-footerRows, 1)
	first := max(panel.cursor-available+1, 0)
	var body []string
	for i := first; i < len(rows) && len(body) < available; i++ {
		value := describeSettingValue(rows[i].Value)
		scope := describeSettingScope(rows[i].Scope)
		row := "  " + rows[i].Label + ": " + value
		if width >= 60 {
			row += " · " + scope
		}
		if i == panel.cursor && !panel.isEditing {
			row = theme.selectedRow.Render("▸" + strings.TrimPrefix(row, " "))
		}
		body = append(body, strings.Split(wrapWithMargin(row, width), "\n")...)
		if width < 60 {
			body = append(body, strings.Split(wrapWithMargin(theme.dim.Render("    "+scope), width), "\n")...)
		}
	}
	if len(rows) == 0 {
		body = append(body, truncateWithMargin(theme.dim.Render("No matching settings"), width))
	}
	lines = append(lines, body[:min(len(body), available)]...)
	for len(lines) < height-footerRows {
		lines = append(lines, "")
	}
	if panel.notice != "" {
		var status ReportStatus
		if strings.HasPrefix(panel.notice, "✗") {
			status = ReportError
		} else if panel.pending {
			status = ReportLoading
		} else {
			status = ReportSuccess
		}
		lines = append(lines, truncateWithMargin(stylePanelLine(panel.notice, status, theme), width))
	}
	lines = append(lines, truncateWithMargin(theme.dim.Render(legend), width))
	return strings.Join(lines, "\n")
}

func describeSettingValue(value string) string {
	switch value {
	case "true", "on":
		return "Enabled"
	case "false", "off":
		return "Disabled"
	default:
		return value
	}
}

func describeSettingScope(scope string) string {
	switch {
	case strings.HasPrefix(scope, "saved"):
		return "Saved"
	case strings.HasPrefix(scope, "this session"):
		return "This session"
	default:
		return scope
	}
}
