package terminal

import (
	"context"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

type confirmation struct {
	title  string
	accept func(context.Context) tea.Msg
}

func (m chatModel) handleConfirmationKey(key tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch key.String() {
	case "y", "enter":
		confirm := m.confirmation
		m.confirmation = nil
		ctx := m.ctx
		return m, func() tea.Msg { return confirm.accept(ctx) }
	case "n", "esc", "ctrl+c":
		m.confirmation = nil
		m.refreshTranscript()
	}
	return m, nil
}

func renderConfirmation(confirm confirmation, width int, theme terminalTheme) string {
	legend := "y confirm · n cancel"
	available := max(width-1-len(legend)-2, 1)
	message := ansi.Truncate(confirm.title, available, ellipsis)
	return truncateWithMargin(theme.warning.Render(message)+"  "+theme.dim.Render(legend), width)
}
