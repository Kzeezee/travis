package terminal

import (
	"context"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/Kzeezee/travis/internal/core"
)

func TestDraggingOverTheTranscriptSelectsAndCopiesThatText(t *testing.T) {
	press := func(x, y int) tea.Msg { return tea.MouseClickMsg{X: x, Y: y, Button: tea.MouseLeft} }
	drag := func(x, y int) tea.Msg { return tea.MouseMotionMsg{X: x, Y: y, Button: tea.MouseLeft} }
	release := tea.MouseReleaseMsg{Button: tea.MouseNone}

	// Row 0 renders as " you › copy the path please"; "copy" starts at cell 7.
	for name, gesture := range map[string][]tea.Msg{
		"forward":  {press(7, 0), drag(10, 0), drag(14, 0), release},
		"backward": {press(14, 0), drag(7, 0), release},
	} {
		t.Run(name, func(t *testing.T) {
			m := buildChatModel(context.Background(), make(chan core.Event), nil, ChatActions{}, Options{ModelName: "gpt-x"})
			m.resize(60, 12)
			m.transcript.startTurn("copy the path please")
			m.refreshTranscript()

			m, cmd := pressKeys(m, gesture...)
			if got := copySelectedText(m.viewport.GetContent(), m.selection); got != "copy the" || cmd == nil {
				t.Fatalf("got selection %q, copy command %v; want \"copy the\" copied", got, cmd != nil)
			}
			if view := renderPlainView(m); !strings.Contains(view, "⧉ copied") {
				t.Fatalf("status bar does not confirm the copy:\n%s", view)
			}
			if !strings.Contains(m.View().Content, selectedRowStyle.Render("copy the")) {
				t.Fatal("the selected cells are not highlighted")
			}
		})
	}

	t.Run("a click without a drag copies nothing", func(t *testing.T) {
		m := buildChatModel(context.Background(), make(chan core.Event), nil, ChatActions{}, Options{ModelName: "gpt-x"})
		m.transcript.startTurn("copy the path please")
		m.refreshTranscript()
		m, cmd := pressKeys(m, press(7, 0), release)
		if cmd != nil || m.selection.isActive {
			t.Fatalf("a plain click selected text: active=%v, command=%v", m.selection.isActive, cmd != nil)
		}
	})
}

func TestCopySelectedTextSpansRowsWithoutStylingOrPadding(t *testing.T) {
	content := " " + userLabelStyle.Render("you ›") + " first line   \n second line\n third line"
	selection := textSelection{anchor: cellPos{row: 0, col: 7}, head: cellPos{row: 2, col: 6}, isActive: true}
	if got, want := copySelectedText(content, selection), "first line\n second line\n third"; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}
