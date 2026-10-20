package terminal

import (
	"fmt"
	"slices"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/Kzeezee/travis/internal/core"
)

// undoModes is the order tab cycles through; the first is the default (/rewind-style).
var undoModes = []core.RewindMode{core.RewindFilesAndConversation, core.RewindConversationOnly, core.RewindFilesOnly}

const undoPickerLegend = "↑↓ choose · tab mode · ⏎ rewind · esc cancel"

// undoPicker is /undo's choice: which turn to rewind to before, newest first, and what to take back.
type undoPicker struct {
	turns  []core.TurnSummary
	cursor int
	mode   core.RewindMode
}

type turnsListedMsg struct {
	turns []core.TurnSummary
	err   error
}

type rewindFinishedMsg struct {
	fromTurn core.ID
	mode     core.RewindMode
	report   core.RewindReport
	err      error
}

func (m chatModel) requestTurnList() (chatModel, tea.Cmd) {
	if m.actions.Undo.ListTurns == nil || m.actions.Undo.Rewind == nil {
		m.transcript.addNotice("undo is not available in this session")
		m.refreshTranscript()
		return m, nil
	}
	listTurns, ctx := m.actions.Undo.ListTurns, m.ctx
	return m, func() tea.Msg {
		turns, err := listTurns(ctx)
		return turnsListedMsg{turns: turns, err: err}
	}
}

func (m chatModel) openUndoPicker(msg turnsListedMsg) (tea.Model, tea.Cmd) {
	switch {
	case msg.err != nil:
		m.transcript.addNotice("✗ undo: " + cutFirstLine(msg.err.Error()))
	case len(msg.turns) == 0:
		m.transcript.addNotice("nothing to undo yet")
	default:
		newestFirst := slices.Clone(msg.turns)
		slices.Reverse(newestFirst)
		m.undoPicker = &undoPicker{turns: newestFirst, mode: undoModes[0]}
	}
	m.refreshTranscript()
	return m.muteDictationForModal()
}

// handleUndoPickerKey takes every key while the picker is open, so nothing typed reaches the input.
func (m chatModel) handleUndoPickerKey(key tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	picker := *m.undoPicker
	switch key.String() {
	case "up", "k":
		picker.cursor = max(picker.cursor-1, 0)
	case "down", "j":
		picker.cursor = min(picker.cursor+1, len(picker.turns)-1)
	case "tab":
		picker.mode = undoModes[(slices.Index(undoModes, picker.mode)+1)%len(undoModes)]
	case "esc", "ctrl+c":
		m.undoPicker = nil
		return m, nil
	case "enter":
		m.undoPicker = nil
		fromTurn, mode := picker.turns[picker.cursor].Turn, picker.mode
		rewind, ctx := m.actions.Undo.Rewind, m.ctx
		return m, func() tea.Msg {
			report, err := rewind(ctx, fromTurn, mode)
			return rewindFinishedMsg{fromTurn: fromTurn, mode: mode, report: report, err: err}
		}
	}
	m.undoPicker = &picker
	return m, nil
}

// finishRewind shows what /undo did. A rewound conversation also leaves its first prompt in
// the input, to edit and send again.
func (m chatModel) finishRewind(msg rewindFinishedMsg) (tea.Model, tea.Cmd) {
	if msg.err != nil {
		m.transcript.addNotice("✗ undo failed: " + cutFirstLine(msg.err.Error()))
		m.refreshTranscript()
		return m, nil
	}
	notice := describeRewind(msg.mode, msg.report.Files)
	if !msg.mode.IsRewindingConversation() {
		m.transcript.addNotice(notice)
		m.refreshTranscript()
		return m, nil
	}
	if prompt, isFound := m.transcript.rewindTurns(msg.fromTurn, notice); isFound {
		m.input.SetValue(prompt)
		m.input.CursorEnd()
	}
	m.refreshTranscript()
	m.viewport.GotoBottom()
	return m, nil
}

func describeRewind(mode core.RewindMode, files core.FileReverts) string {
	parts := []string{"↺ rewound the conversation"}
	if mode == core.RewindFilesOnly {
		parts = []string{"↺ put files back; the conversation is kept"}
	}
	if mode.IsRevertingFiles() {
		if len(files.Restored)+len(files.Deleted)+len(files.Skipped) == 0 {
			parts = append(parts, "no files to put back")
		}
		if len(files.Restored) > 0 {
			parts = append(parts, "restored "+strings.Join(files.Restored, ", "))
		}
		if len(files.Deleted) > 0 {
			parts = append(parts, "deleted "+strings.Join(files.Deleted, ", "))
		}
		for _, skipped := range files.Skipped {
			parts = append(parts, "left "+skipped.Path+" ("+skipped.Reason+")")
		}
	} else {
		parts = append(parts, "files stay as they are")
	}
	return strings.Join(parts, " · ")
}

// renderUndoPicker fills the transcript area: turns newest first, the mode, what the choice
// would take back, and the keys. Rows scroll to keep the cursor visible.
func renderUndoPicker(picker undoPicker, width, height int) string {
	footer := []string{
		"",
		truncateWithMargin(renderUndoModes(picker.mode), width),
		truncateWithMargin(dimStyle.Render(describeUndoChoice(picker)), width),
		truncateWithMargin(dimStyle.Render(undoPickerLegend), width),
	}
	rowsAvailable := max(height-1-len(footer), 1)
	first := max(picker.cursor-rowsAvailable+1, 0)
	lines := []string{truncateWithMargin(assistantLabelStyle.Render("/undo · rewind to before…"), width)}
	for i := first; i < len(picker.turns) && i < first+rowsAvailable; i++ {
		lines = append(lines, truncateWithMargin(renderUndoRow(picker.turns[i], i == picker.cursor, width), width))
	}
	for len(lines) < height-len(footer) {
		lines = append(lines, "")
	}
	return strings.Join(append(lines, footer...), "\n")
}

func renderUndoRow(turn core.TurnSummary, isSelected bool, width int) string {
	marker := "  "
	if isSelected {
		marker = "▸ "
	}
	files := "—"
	switch count := len(turn.ChangedPaths); {
	case count == 1:
		files = "1 file"
	case count > 1:
		files = fmt.Sprintf("%d files", count)
	}
	if turn.HasIrreversible {
		files += " (some can't be undone)"
	}
	prefix := marker + turn.At.Local().Format("15:04") + "  "
	suffix := "  " + files
	promptWidth := max(width-1-ansi.StringWidth(prefix)-ansi.StringWidth(suffix), 1)
	prompt := ansi.Truncate(strings.Join(strings.Fields(turn.Prompt), " "), promptWidth, ellipsis)
	row := prefix + prompt + strings.Repeat(" ", max(promptWidth-ansi.StringWidth(prompt), 0)) + suffix
	if isSelected {
		return userLabelStyle.Render(row)
	}
	return row
}

func renderUndoModes(selected core.RewindMode) string {
	labels := map[core.RewindMode]string{
		core.RewindFilesAndConversation: "files + conversation",
		core.RewindConversationOnly:     "conversation only",
		core.RewindFilesOnly:            "files only",
	}
	options := make([]string, 0, len(undoModes))
	for _, mode := range undoModes {
		mark := "○ "
		if mode == selected {
			mark = "◉ "
		}
		options = append(options, mark+labels[mode])
	}
	return strings.Join(options, "   ")
}

// describeUndoChoice previews the choice: the selected turn and every newer one go.
func describeUndoChoice(picker undoPicker) string {
	rewound := picker.turns[:picker.cursor+1]
	var paths []string
	for _, turn := range rewound {
		for _, path := range turn.ChangedPaths {
			if !slices.Contains(paths, path) {
				paths = append(paths, path)
			}
		}
	}
	turns := "1 turn"
	if len(rewound) > 1 {
		turns = fmt.Sprintf("%d turns", len(rewound))
	}
	files := "no files to put back"
	if len(paths) > 0 {
		files = "puts back " + summarisePaths(paths)
	}
	switch picker.mode {
	case core.RewindConversationOnly:
		return "rewinds " + turns + " · files stay as they are"
	case core.RewindFilesOnly:
		return files + " from " + turns + " · the conversation is kept"
	case core.RewindFilesAndConversation:
	}
	return "rewinds " + turns + " · " + files
}

func summarisePaths(paths []string) string {
	const shown = 2
	if len(paths) <= shown {
		return strings.Join(paths, ", ")
	}
	return strings.Join(paths[:shown], ", ") + fmt.Sprintf(", +%d", len(paths)-shown)
}
