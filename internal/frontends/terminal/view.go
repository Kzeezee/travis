package terminal

import (
	"cmp"
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/Kzeezee/travis/internal/core"
)

const ellipsis = "…"

// Colours only add emphasis; Bubble Tea downsamples them to what the terminal supports.
var (
	userLabelStyle      = lipgloss.NewStyle().Bold(true)
	assistantLabelStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("6"))
	dimStyle            = lipgloss.NewStyle().Faint(true)
	errorStyle          = lipgloss.NewStyle().Foreground(lipgloss.Color("1"))
	warningStyle        = lipgloss.NewStyle().Foreground(lipgloss.Color("3"))
	failureBarStyle     = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("15")).Background(lipgloss.Color("1"))
	selectedRowStyle    = lipgloss.NewStyle().Reverse(true)
)

func (m chatModel) View() tea.View {
	separator := dimStyle.Render(strings.Repeat("─", m.width))
	statusBar := fmt.Sprintf(" %s · %s · last turn: %s", m.modelName, m.reportTurnState(), reportOutcome(m.lastTurnOutcome))

	inputLine := m.input.View()
	if m.pendingApproval != nil {
		inputLine = renderApprovalModal(*m.pendingApproval, m.width)
	}
	statusLine := dimStyle.Render(ansi.Truncate(statusBar, m.width, ellipsis))
	if m.brokerFailure != "" {
		// Fail closed, visibly (§5.12.5): the red bar replaces the status bar for the rest of the session.
		failure := " security broker unreachable, every tool will fail; restart travis · " + m.brokerFailure
		statusLine = failureBarStyle.Width(m.width).Render(ansi.Truncate(failure, m.width, ellipsis))
	}
	rows := m.listPaletteRows()
	sections := []string{m.viewport.View()}
	if m.undoPicker != nil {
		sections[0] = renderUndoPicker(*m.undoPicker, m.width, m.viewport.Height())
	}
	if len(rows) > 0 {
		sections = append(sections, m.renderPalette(rows))
	}
	view := tea.NewView(strings.Join(append(sections,
		separator,
		inputLine,
		separator,
		statusLine,
	), "\n"))
	view.AltScreen = true
	view.MouseMode = tea.MouseModeCellMotion
	if cursor := m.input.Cursor(); cursor != nil && m.pendingApproval == nil && m.undoPicker == nil {
		cursor.Y += m.viewport.Height() + len(rows) + 1
		view.Cursor = cursor
	}
	return view
}

// approvalLegend is the only three answers the keyboard accepts; it must always be fully
// visible, so renderApprovalModal truncates the explanation around it rather than
// truncating the whole line and risking cutting the legend off. An irreversible operation
// offers no always (§6.4), so its legend leaves it out.
const (
	approvalLegend             = "[y] once · [a] always · [n] deny"
	irreversibleApprovalLegend = "can't be undone · [y] once · [n] deny"
)

// renderApprovalModal replaces the input line while a decision is pending (D-10): it shows
// what is being asked, why (policy.Explain's trace, flattened like a collapsed thought so
// it still fits the one-line input row), and the legend, with the legend always kept whole.
func renderApprovalModal(request core.ApprovalRequest, width int) string {
	op := request.Operation
	explanation := strings.Join(strings.Fields(op.Explanation), " ")
	prefix := fmt.Sprintf(" %s %s — %s · ", op.Kind, op.Target, explanation)

	legend := approvalLegend
	if op.IsIrreversible {
		legend = irreversibleApprovalLegend
	}
	available := max(width-1-lipgloss.Width(legend), lipgloss.Width(ellipsis))
	line := ansi.Truncate(prefix, available, ellipsis) + legend
	return assistantLabelStyle.Render(ansi.Truncate(line, max(width-1, 1), ellipsis))
}

// renderPalette lists matching commands; the selected row is marked and reversed so it shows without colour too.
func (m chatModel) renderPalette(rows []slashCommand) string {
	lines := make([]string, 0, len(rows))
	for i, command := range rows {
		label := fmt.Sprintf("%-16s %s", command.usage, command.summary)
		if i == m.paletteIndex {
			lines = append(lines, truncateWithMargin(selectedRowStyle.Render("› "+label), m.width))
		} else {
			lines = append(lines, truncateWithMargin(dimStyle.Render("  "+label), m.width))
		}
	}
	return strings.Join(lines, "\n")
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
	sections := make([]string, 0, len(t.turns)+1)
	if len(t.warnings) > 0 {
		warnings := make([]string, 0, len(t.warnings))
		for _, warning := range t.warnings {
			warnings = append(warnings, wrapWithMargin(warningStyle.Render("⚠ "+warning), width))
		}
		sections = append(sections, strings.Join(warnings, "\n"))
	}
	for _, turn := range t.turns {
		switch {
		case !turn.isRewound:
			sections = append(sections, renderTurn(turn, width))
		case len(turn.notices) > 0: // a rewound turn shows only what /undo did, in its place
			sections = append(sections, renderNotices(turn.notices, width))
		}
	}
	return strings.Join(sections, "\n\n")
}

// renderTurn lays a turn out as the prompt, then replies, thoughts, and tool cards in arrival order.
// Only replies carry the travis label; thoughts and cards stay faint so they never read as the answer.
func renderTurn(turn chatTurn, width int) string {
	lines := []string{wrapWithMargin(userLabelStyle.Render("you ›")+" "+turn.prompt, width), ""}

	for _, part := range turn.parts {
		reply := strings.TrimSpace(part.text)
		switch {
		case part.tool != nil:
			lines = append(lines, truncateWithMargin(renderToolCard(*part.tool), width))
		case part.isThought:
			lines = append(lines, truncateWithMargin(renderCollapsedThought(part.text), width))
		case reply != "": // a whitespace-only chunk before a tool call must not show a bare label
			lines = append(lines, wrapWithMargin(assistantLabelStyle.Render("travis ›")+" "+reply, width))
		}
	}

	switch turn.outcome {
	case turnFailed:
		lines = append(lines, truncateWithMargin(errorStyle.Render("  ✗ error: "+cutFirstLine(turn.failure)), width))
	case turnInterrupted:
		lines = append(lines, truncateWithMargin(dimStyle.Render("  ⊘ interrupted"), width))
	case turnPending, turnSucceeded:
	}
	if len(turn.changedPaths) > 0 && turn.outcome != turnPending {
		changed := "  ✎ changed " + strings.Join(turn.changedPaths, ", ") + " · /undo to take back"
		lines = append(lines, truncateWithMargin(dimStyle.Render(changed), width))
	}
	if len(turn.notices) > 0 {
		lines = append(lines, renderNotices(turn.notices, width))
	}
	return strings.Join(lines, "\n")
}

func renderNotices(notices []string, width int) string {
	lines := make([]string, 0, len(notices))
	for _, notice := range notices {
		lines = append(lines, wrapWithMargin(warningStyle.Render("  "+notice), width))
	}
	return strings.Join(lines, "\n")
}

// renderCollapsedThought flattens reasoning onto one line; the caller truncates it to fit (DESIGN.md §5.9 show_reasoning = "collapsed").
func renderCollapsedThought(thought string) string {
	return dimStyle.Render("  ∴ thinking: " + strings.Join(strings.Fields(thought), " "))
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
