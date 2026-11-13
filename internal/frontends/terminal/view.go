package terminal

import (
	"cmp"
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/glamour/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/Kzeezee/travis/internal/core"
)

const ellipsis = "…"

// unsandboxedTier is the tier name for an MCP server running unconfined (require_sandbox = false).
const unsandboxedTier = "none"

// recordSandboxTier keeps the unsandboxed tier once any server ran unconfined, so a later sandboxed one never hides it.
func recordSandboxTier(current, reported string) string {
	if current == unsandboxedTier {
		return current
	}
	return reported
}

func reportSandboxStatus(tier string) string {
	switch tier {
	case "":
		return ""
	case unsandboxedTier:
		return " · " + warningStyle.Render("⚠ sandbox: none")
	default:
		return " · sandbox: " + tier
	}
}

// reportMCPHealth is empty while every server is fine; the glyph keeps it visible without colour.
func reportMCPHealth(unavailable map[string]bool) string {
	switch len(unavailable) {
	case 0:
		return ""
	case 1:
		for name := range unavailable {
			return " · " + errorStyle.Render("● mcp "+name)
		}
	}
	return " · " + errorStyle.Render(fmt.Sprintf("● mcp %d down", len(unavailable)))
}

// renderStatusBar leads with the lantern mark and tints only the words that change: turn state and outcome.
func (m chatModel) renderStatusBar() string {
	state := m.reportTurnState()
	stateStyle := dimStyle
	switch state {
	case "streaming":
		stateStyle = assistantLabelStyle
	case "interrupted":
		stateStyle = warningStyle
	}
	outcomeStyle := dimStyle
	switch m.lastTurnOutcome {
	case turnSucceeded:
		outcomeStyle = successStyle
	case turnFailed:
		outcomeStyle = errorStyle
	case turnInterrupted:
		outcomeStyle = warningStyle
	case turnPending:
	}
	copied := ""
	if m.selection.isActive && !m.selection.isDragging {
		copied = dimStyle.Render(" · ") + assistantLabelStyle.Render("⧉ copied")
	}
	status := assistantLabelStyle.Render(" ◆") + " " + lipgloss.NewStyle().Bold(true).Render(m.modelName) +
		dimStyle.Render(" · ") + stateStyle.Render(state) +
		dimStyle.Render(" · last turn: ") + outcomeStyle.Render(reportOutcome(m.lastTurnOutcome)) + copied +
		dimStyle.Render(reportSandboxStatus(m.sandboxTier)+m.reportDictationStatus()+m.reportSpendStatus())
	status += reportMCPHealth(m.unavailableMCPServers)
	return status + dimStyle.Render(m.reportTokenUsage(ansi.StringWidth(status)))
}

// reportSpendStatus shows today against the daily ceiling, or the last turn against the turn ceiling when only that is set.
func (m chatModel) reportSpendStatus() string {
	switch {
	case m.maxDailyCostUSD > 0:
		return fmt.Sprintf(" · $%.2f/$%.2f", m.spentTodayUSD, m.maxDailyCostUSD)
	case m.maxTurnCostUSD > 0:
		return fmt.Sprintf(" · $%.2f/$%.2f turn", m.turnCostUSD, m.maxTurnCostUSD)
	case m.isPriced:
		return fmt.Sprintf(" · ~$%.2f", m.transcript.costUSD)
	default:
		return ""
	}
}

const contextBarCells = 10

// reportTokenUsage is the context-fill and session-total segment; the bar is the first thing
// dropped when it would not fit beside the rest of the status bar, which is usedWidth wide.
func (m chatModel) reportTokenUsage(usedWidth int) string {
	if m.contextTokens == 0 {
		return ""
	}
	fill := formatTokenCount(m.contextTokens)
	if m.contextLength > 0 {
		fill += "/" + formatTokenCount(m.contextLength)
	}
	session := fmt.Sprintf(" · ↑%s ↓%s", formatTokenCount(m.transcript.usage.InputTokens), formatTokenCount(m.transcript.usage.OutputTokens))
	compact := " · ctx " + fill + session
	if m.contextLength <= 0 {
		return compact
	}
	filledCells := min(contextBarCells, (m.contextTokens*contextBarCells+m.contextLength/2)/m.contextLength)
	bar := strings.Repeat("█", filledCells) + strings.Repeat("░", contextBarCells-filledCells)
	withBar := " · ctx " + bar + " " + fill + session
	if usedWidth+ansi.StringWidth(withBar) > m.width {
		return compact
	}
	return withBar
}

// formatTokenCount abbreviates thousands to one decimal, dropping a trailing ".0": 24100 is "24.1k", 32000 is "32k".
func formatTokenCount(tokens int) string {
	if tokens < 1000 {
		return fmt.Sprint(tokens)
	}
	return strings.TrimSuffix(fmt.Sprintf("%.1f", float64(tokens)/1000), ".0") + "k"
}

func (m chatModel) View() tea.View {
	separator := ruleStyle.Render(strings.Repeat("─", m.width))

	inputLine := m.input.View()
	if m.pendingApproval != nil {
		inputLine = renderApprovalModal(*m.pendingApproval, m.width)
	}
	statusLine := ansi.Truncate(m.renderStatusBar(), m.width, ellipsis)
	if m.brokerFailure != "" {
		// Fail closed, visibly (§5.12.5): the red bar replaces the status bar for the rest of the session.
		failure := " security broker unreachable, every tool will fail; restart travis · " + m.brokerFailure
		statusLine = failureBarStyle.Width(m.width).Render(ansi.Truncate(failure, m.width, ellipsis))
	}
	rows := m.listPaletteRows()
	sections := []string{highlightSelection(m.viewport.View(), m.selection, m.viewport.YOffset())}
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
	// Hold-to-talk needs key releases; terminals without them report so, and push-to-talk toggles.
	view.KeyboardEnhancements.ReportEventTypes = m.isDictationAvailable()
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
	// Stripped so no escape sequence in a target or explanation can hide what is approved.
	prefix := ansi.Strip(fmt.Sprintf(" %s %s — %s · ", op.Kind, op.Target, explanation))

	legend := approvalLegend
	if op.IsIrreversible {
		legend = irreversibleApprovalLegend
	}
	available := max(width-1-lipgloss.Width(legend), lipgloss.Width(ellipsis))
	line := ansi.Truncate(prefix, available, ellipsis) + legend
	return assistantLabelStyle.Render(ansi.Truncate(line, max(width-1, 1), ellipsis))
}

// renderPalette lists matching commands or models; the selected row is marked and reversed so it shows without colour too.
func (m chatModel) renderPalette(rows []paletteRow) string {
	lines := make([]string, 0, len(rows))
	for i, row := range rows {
		if i == m.paletteIndex {
			lines = append(lines, truncateWithMargin(selectedRowStyle.Render("› "+row.label), m.width))
		} else {
			lines = append(lines, truncateWithMargin(dimStyle.Render("  "+row.label), m.width))
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

func renderTranscript(t transcript, width int, replies *replyRenderer) string {
	sections := make([]string, 0, len(t.turns)+2)
	if len(t.warnings) > 0 {
		warnings := make([]string, 0, len(t.warnings))
		for _, warning := range t.warnings {
			warnings = append(warnings, wrapWithMargin(warningStyle.Render("⚠ "+warning), width))
		}
		sections = append(sections, strings.Join(warnings, "\n"))
	}
	if len(t.notices) > 0 {
		sections = append(sections, renderNotices(t.notices, width))
	}
	if len(t.turns) == 0 && len(t.notices) == 0 {
		sections = append(sections, renderWelcome(width))
	}
	for _, turn := range t.turns {
		switch {
		case !turn.isRewound:
			sections = append(sections, renderTurn(turn, width, replies))
		case len(turn.notices) > 0: // a rewound turn shows only what /undo did, in its place
			sections = append(sections, renderNotices(turn.notices, width))
		}
	}
	return strings.Join(sections, "\n\n")
}

// renderTurn lays a turn out as the prompt, then replies, thoughts, and tool cards in arrival order.
// Only replies carry the travis label; thoughts and cards stay faint so they never read as the answer.
func renderTurn(turn chatTurn, width int, replies *replyRenderer) string {
	lines := []string{wrapWithMargin(userLabelStyle.Render("you ›")+" "+turn.prompt, width), ""}

	for _, part := range turn.parts {
		reply := strings.TrimSpace(part.text)
		switch {
		case part.tool != nil:
			lines = append(lines, truncateWithMargin(renderToolCard(*part.tool), width))
		case part.isThought:
			lines = append(lines, truncateWithMargin(renderCollapsedThought(part.text), width))
		case reply != "": // a whitespace-only chunk before a tool call must not show a bare label
			lines = append(lines, replies.renderReply(reply, width))
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

// renderWelcome fills the empty transcript with the mark and the gestures nothing else on screen teaches.
func renderWelcome(width int) string {
	return wrapWithMargin(assistantLabelStyle.Render("◆ travis"), width) + "\n" +
		wrapWithMargin(dimStyle.Render("  / for commands · drag to copy · wheel or PgUp/PgDn to scroll"), width)
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
	return thoughtStyle.Render("  ∴ thinking: " + strings.Join(strings.Fields(thought), " "))
}

// renderToolCard tints the status glyph and tool name; the detail stays steel so cards never outshine replies.
func renderToolCard(card toolCard) string {
	switch card.status {
	case toolRunning:
		return toolStyle.Render("  ⚙ "+card.name) + dimStyle.Render(" (running…)")
	case toolSucceeded:
		return successStyle.Render("  ✓ ") + dimStyle.Render(cmp.Or(card.detail, card.name))
	case toolFailed:
		return errorStyle.Render("  ✗ " + card.name + ": " + card.detail)
	}
	return ""
}

const assistantLabel = "travis ›"

// ponytail: cache dropped wholesale at this size (each streamed frame adds a dead entry); LRU if re-rendering history after a drop is ever felt.
const maxCachedReplies = 256

type markdownCacheKey struct {
	text  string
	width int
}

// replyRenderer lays assistant replies out beside the travis label: as markdown, or as plain
// wrapped text when the terminal has under 256 colours. The zero value renders markdown.
type replyRenderer struct {
	isPlainTextOnly bool
	cache           map[markdownCacheKey]string
}

// renderReply labels a reply and fits it in the transcript width. Markdown lines hang under the
// first line's text so blocks (lists, fences) stay aligned; an unclosed fence renders as code, and a
// markdown failure falls back to plain text.
func (r *replyRenderer) renderReply(reply string, width int) string {
	label := assistantLabelStyle.Render(assistantLabel) + " "
	if r.isPlainTextOnly {
		return wrapWithMargin(label+reply, width)
	}
	indent := lipgloss.Width(assistantLabel) + 1
	key := markdownCacheKey{reply, width}
	body, isCached := r.cache[key]
	if !isCached {
		var err error
		if body, err = renderMarkdown(reply, max(width-1-indent, 1)); err != nil {
			return wrapWithMargin(label+reply, width)
		}
		if r.cache == nil || len(r.cache) >= maxCachedReplies {
			r.cache = make(map[markdownCacheKey]string)
		}
		r.cache[key] = body
	}
	lines := strings.Split(body, "\n")
	for i := 1; i < len(lines); i++ {
		if lines[i] != "" {
			lines[i] = strings.Repeat(" ", indent+1) + lines[i]
		}
	}
	return " " + label + strings.Join(lines, "\n")
}

func renderMarkdown(text string, wrapWidth int) (string, error) {
	renderer, err := glamour.NewTermRenderer(glamour.WithStyles(replyMarkdownStyle), glamour.WithWordWrap(wrapWidth), glamour.WithPreservedNewLines())
	if err != nil {
		return "", err
	}
	rendered, err := renderer.Render(text)
	if err != nil {
		return "", err
	}
	return strings.Trim(rendered, "\n"), nil
}

// wrapWithMargin wraps text inside a one-cell left margin.
func wrapWithMargin(text string, width int) string {
	wrapped := lipgloss.Wrap(text, max(width-1, 1), "")
	return " " + strings.ReplaceAll(wrapped, "\n", "\n ")
}

func truncateWithMargin(line string, width int) string {
	return " " + ansi.Truncate(line, max(width-1, 1), ellipsis)
}
