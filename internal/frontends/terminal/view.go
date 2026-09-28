package terminal

import (
	"cmp"
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

const ellipsis = "…"

// Colours only add emphasis; Bubble Tea downsamples them to what the terminal supports.
var (
	userLabelStyle      = lipgloss.NewStyle().Bold(true)
	assistantLabelStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("6"))
	dimStyle            = lipgloss.NewStyle().Faint(true)
	errorStyle          = lipgloss.NewStyle().Foreground(lipgloss.Color("1"))
)

func (m chatModel) View() tea.View {
	separator := dimStyle.Render(strings.Repeat("─", m.width))
	statusBar := fmt.Sprintf(" %s · %s · last turn: %s", m.modelName, m.reportTurnState(), reportOutcome(m.lastTurnOutcome))

	view := tea.NewView(strings.Join([]string{
		m.viewport.View(),
		separator,
		m.input.View(),
		separator,
		dimStyle.Render(ansi.Truncate(statusBar, m.width, ellipsis)),
	}, "\n"))
	view.AltScreen = true
	view.MouseMode = tea.MouseModeCellMotion
	if cursor := m.input.Cursor(); cursor != nil {
		cursor.Y += m.viewport.Height() + 1
		view.Cursor = cursor
	}
	return view
}

func (m chatModel) reportTurnState() string {
	switch {
	case m.cancelTurn == nil:
		return "idle"
	case m.isCancelRequested:
		return "interrupted"
	default:
		return "streaming"
	}
}

func reportOutcome(outcome turnOutcome) string {
	switch outcome {
	case turnPending:
		return "none"
	case turnSucceeded:
		return "ok"
	case turnFailed:
		return "error"
	case turnInterrupted:
		return "interrupted"
	}
	return ""
}

func renderTranscript(t transcript, width int) string {
	turns := make([]string, 0, len(t.turns))
	for _, turn := range t.turns {
		turns = append(turns, renderTurn(turn, width))
	}
	return strings.Join(turns, "\n\n")
}

// renderTurn lays a turn out as the prompt, then assistant text and tool cards in arrival order.
func renderTurn(turn chatTurn, width int) string {
	lines := []string{wrapWithMargin(userLabelStyle.Render("you ›")+" "+turn.prompt, width), ""}

	// The trailing space is trimmed on flush when a tool card follows the label directly.
	text := assistantLabelStyle.Render("travis ›") + " "
	for _, part := range turn.parts {
		if part.tool == nil {
			text += part.text
			continue
		}
		if text != "" {
			lines = append(lines, wrapWithMargin(strings.TrimRight(text, " \n"), width))
			text = ""
		}
		lines = append(lines, truncateWithMargin(renderToolCard(*part.tool), width))
	}
	if text != "" {
		lines = append(lines, wrapWithMargin(strings.TrimRight(text, " \n"), width))
	}

	switch turn.outcome {
	case turnFailed:
		lines = append(lines, truncateWithMargin(errorStyle.Render("  ✗ error: "+cutFirstLine(turn.failure)), width))
	case turnInterrupted:
		lines = append(lines, truncateWithMargin(dimStyle.Render("  ⊘ interrupted"), width))
	case turnPending, turnSucceeded:
	}
	return strings.Join(lines, "\n")
}

func renderToolCard(card toolCard) string {
	switch card.status {
	case toolRunning:
		return dimStyle.Render("  ⚙ " + card.name + " (running…)")
	case toolSucceeded:
		return dimStyle.Render("  ✓ " + cmp.Or(card.detail, card.name))
	case toolFailed:
		return errorStyle.Render("  ✗ " + card.name + ": " + card.detail)
	}
	return ""
}

// wrapWithMargin wraps text inside a one-cell left margin.
func wrapWithMargin(text string, width int) string {
	wrapped := lipgloss.Wrap(text, max(width-1, 1), "")
	return " " + strings.ReplaceAll(wrapped, "\n", "\n ")
}

func truncateWithMargin(line string, width int) string {
	return " " + ansi.Truncate(line, max(width-1, 1), ellipsis)
}
