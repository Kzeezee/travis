package terminal

import (
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/Kzeezee/travis/internal/core"
)

const (
	mascotFrameInterval  = 110 * time.Millisecond
	metricsFrameInterval = time.Second
	startupFrameCount    = 6
	settleFrameCount     = 4
)

type mascotTickMsg struct{ at time.Time }

type mascotState int

const (
	mascotIdle mascotState = iota
	mascotThinking
	mascotTool
	mascotListening
	mascotApproval
	mascotSuccess
	mascotFailure
)

func scheduleMascotTick(clock core.Clock, interval time.Duration) tea.Cmd {
	return func() tea.Msg {
		return mascotTickMsg{at: <-clock.After(interval)}
	}
}

func (m chatModel) reportMascotState() mascotState {
	switch {
	case m.pendingApproval != nil:
		return mascotApproval
	case m.dictation != dictationIdle:
		return mascotListening
	case m.hasRunningTool():
		return mascotTool
	case m.cancelTurn != nil:
		return mascotThinking
	case m.settleFrames > 0 && m.lastTurnOutcome == turnSucceeded:
		return mascotSuccess
	case m.settleFrames > 0 && m.lastTurnOutcome == turnFailed:
		return mascotFailure
	default:
		return mascotIdle
	}
}

func (m chatModel) hasRunningTool() bool {
	if len(m.transcript.turns) == 0 {
		return false
	}
	for _, part := range m.transcript.turns[len(m.transcript.turns)-1].parts {
		if part.tool != nil && part.tool.status == toolRunning {
			return true
		}
	}
	return false
}

func (m chatModel) shouldAnimateMascot() bool {
	if m.isReducedMotion || m.pendingApproval != nil {
		return false
	}
	return m.startupFrames > 0 || m.cancelTurn != nil || m.dictation != dictationIdle || m.settleFrames > 0
}

func (m chatModel) shouldTickMascot() bool {
	return m.cancelTurn != nil || m.shouldAnimateMascot()
}

func (m chatModel) reportMascotTickInterval() time.Duration {
	if m.shouldAnimateMascot() {
		return mascotFrameInterval
	}
	return metricsFrameInterval
}

func (m chatModel) renderCompactMascot() string {
	frame := m.mascotFrame
	switch m.reportMascotState() {
	case mascotThinking:
		return []string{"◊", "◆", "◆", "◆"}[frame%4]
	case mascotTool:
		return []string{"◆", "*", "◆", "·"}[frame%4]
	case mascotListening:
		return []string{"◊", "◆", "◆", "◆"}[frame%4]
	case mascotApproval:
		return "!"
	case mascotSuccess:
		return []string{"◆", "*"}[frame%2]
	case mascotFailure:
		return []string{"◆", "◊"}[frame%2]
	case mascotIdle:
		return "◆"
	}
	return "◆"
}

// renderWelcome is the empty chat state: a hero when space permits and the original compact mark otherwise.
func renderWelcome(width, height int, modelName string, isDictationAvailable bool, theme terminalTheme, frame int) string {
	exploreHint := "/ for commands"
	compactHint := "  / commands · PgUp/PgDn scroll"
	if isDictationAvailable {
		exploreHint += " · ctrl+t to talk"
		compactHint = "  / commands · ctrl+t talk · PgUp/PgDn scroll"
	}
	if width < 72 || height < 15 {
		return wrapWithMargin(theme.mascotMark.Render("◆")+" "+theme.assistantLabel.Render("travis"), width) + "\n" +
			truncateWithMargin(theme.dim.Render(compactHint), width)
	}

	sprite := renderBeaconBuddy(theme, frame)
	lines := []string{
		"",
		sprite,
		"",
		lipgloss.NewStyle().Bold(true).Render("    T R A V I S"),
		theme.dim.Render("    Personal assistance. Guardrails included."),
		"",
		theme.dim.Render("    model    ") + modelName,
		theme.dim.Render("    begin    ") + "type a message",
		theme.dim.Render("    explore  ") + exploreHint,
	}
	return strings.Join(lines, "\n")
}

func renderBeaconBuddy(theme terminalTheme, frame int) string {
	beaconFill := []string{"█", "▓", "█", "▓"}[frame%4]
	eye := "██"
	if frame%4 == 1 {
		eye = "▄▄"
	}
	lines := []string{
		theme.mascotBeacon.Render("        ▄" + beaconFill + "▄"),
		theme.mascotBeacon.Render("        ▀" + beaconFill + "▀"),
		theme.mascotBody.Render("         ██"),
		theme.mascotBody.Render("    ▄██████████▄"),
		theme.mascotBody.Render(" ▄███  ") + theme.mascotFace.Render(eye) + theme.mascotBody.Render("  ") + theme.mascotFace.Render(eye) + theme.mascotBody.Render("  ███▄"),
		theme.mascotBody.Render(" ██     ") + theme.mascotFace.Render("╰─╯") + theme.mascotBody.Render("     ██"),
		theme.mascotBody.Render(" ▀███▄▄██████▄▄███▀"),
		theme.mascotBody.Render("      ██    ██"),
	}
	return strings.Join(lines, "\n")
}
