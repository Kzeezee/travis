package terminal

import (
	"strings"

	tea "charm.land/bubbletea/v2"
)

type commandPanelKind string

const (
	commandPanelHelp        commandPanelKind = "help"
	commandPanelTheme       commandPanelKind = "theme"
	commandPanelModel       commandPanelKind = "model"
	commandPanelProvider    commandPanelKind = "provider"
	commandPanelPermissions commandPanelKind = "permissions"
	commandPanelMemory      commandPanelKind = "memory"
	commandPanelSettings    commandPanelKind = "settings"
	commandPanelUndo        commandPanelKind = "undo"
)

type commandPanelOption struct {
	section, label, detail, value string
	status                        ReportStatus
}

type commandPanel struct {
	kind    commandPanelKind
	title   string
	lines   []string
	options []commandPanelOption
	cursor  int
}

func (m chatModel) openCommandPanel(panel commandPanel) chatModel {
	m.commandPanel = &panel
	m.input.Reset()
	m.fitViewport()
	return m
}

func (m chatModel) closeCommandPanel() chatModel {
	m.commandPanel = nil
	m.input.Reset()
	m.fitViewport()
	return m
}

func (m chatModel) handleCommandPanelKey(key tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	panel := *m.commandPanel
	rowCount := len(panel.options)
	if rowCount == 0 {
		rowCount = len(panel.lines)
	}
	switch key.String() {
	case "esc", "ctrl+c":
		return m.closeCommandPanel(), nil
	case "up", "k":
		panel.cursor = max(panel.cursor-1, 0)
	case "down", "j":
		panel.cursor = min(panel.cursor+1, max(rowCount-1, 0))
	case "enter":
		if len(panel.options) > 0 && panel.options[panel.cursor].value != "" {
			return m.activateCommandPanelOption(panel.kind, panel.options[panel.cursor].value)
		}
	}
	m.commandPanel = &panel
	return m, nil
}

func (m chatModel) activateCommandPanelOption(kind commandPanelKind, value string) (tea.Model, tea.Cmd) {
	m = m.closeCommandPanel()
	switch kind {
	case commandPanelTheme:
		return selectTheme(m, value)
	case commandPanelModel:
		return selectModel(m, value)
	case commandPanelProvider:
		return m.requestProviderSwitch(value)
	case commandPanelMemory:
		switch value {
		case "edit", "clear":
			return m.requestMemory(value)
		}
	case commandPanelHelp, commandPanelPermissions, commandPanelSettings, commandPanelUndo:
		return m, nil
	}
	return m, nil
}

func renderCommandPanel(panel commandPanel, width, height int, theme terminalTheme) string {
	legend := "↑↓ choose · enter select · esc close"
	hasSelectableOption := false
	for _, option := range panel.options {
		hasSelectableOption = hasSelectableOption || option.value != ""
	}
	if !hasSelectableOption {
		legend = "↑↓ scroll · esc close"
	}
	if width < 45 {
		legend = "↑↓ choose · enter · esc"
		if !hasSelectableOption {
			legend = "↑↓ scroll · esc"
		}
	}
	lines := []string{truncateWithMargin(theme.assistantLabel.Render("/"+panel.title), width)}
	available := max(height-2, 1)
	var body []string
	if len(panel.options) == 0 {
		first := max(panel.cursor-available+1, 0)
		for index := first; index < len(panel.lines) && index < first+available; index++ {
			body = append(body, renderPanelLine(panel.lines[index], width, theme)...)
		}
	} else {
		first := max(panel.cursor-available+1, 0)
		lastSection := ""
		for index := first; index < len(panel.options) && len(body) < available; index++ {
			option := panel.options[index]
			if option.section != "" && option.section != lastSection {
				body = append(body, truncateWithMargin(theme.assistantLabel.Render(option.section+":"), width))
				lastSection = option.section
			}
			body = append(body, renderPanelOption(option, index == panel.cursor, width, theme)...)
		}
	}
	lines = append(lines, body[:min(len(body), available)]...)
	for len(lines) < height-1 {
		lines = append(lines, "")
	}
	lines = append(lines, truncateWithMargin(theme.dim.Render(legend), width))
	return strings.Join(lines[:min(len(lines), height)], "\n")
}

func renderPanelOption(option commandPanelOption, isSelected bool, width int, theme terminalTheme) []string {
	prefix := reportStatusPrefix(option.status)
	row := "  " + prefix + option.label
	if option.detail != "" && width >= 50 {
		row += " · " + option.detail
	}
	if isSelected {
		row = theme.selectedRow.Render("▸" + strings.TrimPrefix(row, " "))
	} else {
		row = stylePanelLine(row, option.status, theme)
	}
	lines := strings.Split(wrapWithMargin(row, width), "\n")
	if option.detail != "" && width < 50 {
		lines = append(lines, strings.Split(wrapWithMargin(theme.dim.Render("    "+option.detail), width), "\n")...)
	}
	return lines
}

func renderPanelLine(line string, width int, theme terminalTheme) []string {
	return strings.Split(wrapWithMargin(stylePanelLine(line, reportStatusFromPrefix(line), theme), width), "\n")
}

func reportStatusFromPrefix(line string) ReportStatus {
	switch {
	case strings.HasPrefix(strings.TrimSpace(line), "✓ "):
		return ReportSuccess
	case strings.HasPrefix(strings.TrimSpace(line), "✗ "):
		return ReportError
	case strings.HasPrefix(strings.TrimSpace(line), "! "):
		return ReportWarning
	case strings.HasPrefix(strings.TrimSpace(line), "… "):
		return ReportLoading
	case strings.HasPrefix(strings.TrimSpace(line), "○ "):
		return ReportUnavailable
	default:
		return ReportNeutral
	}
}

func stylePanelLine(line string, status ReportStatus, theme terminalTheme) string {
	switch status {
	case ReportCurrent, ReportSuccess:
		return theme.success.Render(line)
	case ReportLoading:
		return theme.tool.Render(line)
	case ReportWarning:
		return theme.warning.Render(line)
	case ReportError:
		return theme.error.Render(line)
	case ReportUnavailable, ReportNeutral:
		if strings.HasSuffix(strings.TrimSpace(line), ":") {
			return theme.assistantLabel.Render(line)
		}
		return theme.dim.Render(line)
	}
	return line
}

func limitPanelRows(content string, height int) string {
	lines := strings.Split(content, "\n")
	height = max(height, 1)
	lines = lines[:min(len(lines), height)]
	for len(lines) < height {
		lines = append(lines, "")
	}
	return strings.Join(lines, "\n")
}
