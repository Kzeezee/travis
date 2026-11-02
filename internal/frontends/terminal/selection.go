package terminal

import (
	"math"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/atotto/clipboard"
	"github.com/charmbracelet/x/ansi"
)

// Mouse capture (needed for wheel scrolling) takes drag-selection away from the terminal, so the
// transcript selects for itself: drag to highlight, release to copy. Shift-drag still reaches the
// terminal's own selection in most emulators.

// cellPos is a cell in the transcript's rendered content, not on screen, so a selection survives scrolling.
type cellPos struct{ row, col int }

// textSelection is a drag over the transcript; anchor is where it began. isActive turns on with
// the first drag motion, so a plain click selects nothing.
type textSelection struct {
	anchor, head cellPos
	isDragging   bool
	isActive     bool
}

// spanOnRow reports the cells [from, to) the selection covers on row; ok is false off the selection.
func (s textSelection) spanOnRow(row int) (from, to int, ok bool) {
	start, end := s.anchor, s.head
	if end.row < start.row || (end.row == start.row && end.col < start.col) {
		start, end = end, start
	}
	if !s.isActive || row < start.row || row > end.row {
		return 0, 0, false
	}
	from, to = 0, math.MaxInt
	if row == start.row {
		from = start.col
	}
	if row == end.row {
		to = end.col + 1
	}
	return from, to, true
}

// handleMouseSelection drives a drag over the transcript; release copies to the system clipboard.
func (m chatModel) handleMouseSelection(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	mouse := msg.Mouse()
	if m.undoPicker != nil {
		return m, nil
	}
	switch msg.(type) {
	case tea.MouseClickMsg:
		m.selection = textSelection{}
		if mouse.Button == tea.MouseLeft && mouse.Y < m.viewport.Height() {
			at := m.locateCell(mouse)
			m.selection = textSelection{anchor: at, head: at, isDragging: true}
		}
	case tea.MouseMotionMsg:
		if m.selection.isDragging {
			m.selection.head, m.selection.isActive = m.locateCell(mouse), true
		}
	case tea.MouseReleaseMsg: // legacy mouse encodings report no button on release
		if !m.selection.isDragging {
			return m, nil
		}
		m.selection.isDragging = false
		if text := copySelectedText(m.viewport.GetContent(), m.selection); text != "" {
			return m, copyToClipboard(text)
		}
		m.selection = textSelection{}
	}
	return m, nil
}

// locateCell maps a screen position to transcript content, clamped to the rendered rows.
func (m chatModel) locateCell(mouse tea.Mouse) cellPos {
	row := min(max(mouse.Y, 0), m.viewport.Height()-1) + m.viewport.YOffset()
	return cellPos{row: min(row, max(m.viewport.TotalLineCount()-1, 0)), col: max(mouse.X, 0)}
}

// copySelectedText is the plain text under the selection, without styling or row padding.
func copySelectedText(content string, selection textSelection) string {
	lines := strings.Split(content, "\n")
	var picked []string
	for row, line := range lines {
		if from, to, ok := selection.spanOnRow(row); ok {
			picked = append(picked, strings.TrimRight(ansi.Cut(ansi.Strip(line), from, to), " "))
		}
	}
	return strings.Trim(strings.Join(picked, "\n"), "\n")
}

// highlightSelection paints the selected cells of the visible transcript; firstRow is the content row at the top.
func highlightSelection(view string, selection textSelection, firstRow int) string {
	if !selection.isActive {
		return view
	}
	lines := strings.Split(view, "\n")
	for i, line := range lines {
		if from, to, ok := selection.spanOnRow(firstRow + i); ok {
			selected := ansi.Strip(ansi.Cut(line, from, to))
			lines[i] = ansi.Truncate(line, from, "") + ansi.ResetStyle + selectedRowStyle.Render(selected) + ansi.TruncateLeft(line, to, "")
		}
	}
	return strings.Join(lines, "\n")
}

// copyToClipboard writes through OSC 52, which works over SSH and in most terminals, and through the
// OS clipboard for terminals without OSC 52 (GNOME Terminal); whichever lands, the paste is the same.
func copyToClipboard(text string) tea.Cmd {
	return tea.Batch(
		tea.SetClipboard(text),
		tea.SetPrimaryClipboard(text),
		func() tea.Msg {
			_ = clipboard.WriteAll(text) // best effort: OSC 52 above already covers most terminals
			return nil
		},
	)
}
