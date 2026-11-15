package terminal

import (
	"context"
	"fmt"
	"strings"

	"charm.land/bubbles/v2/textarea"
	tea "charm.land/bubbletea/v2"
)

// MemoryDocument separates trusted human notes from untrusted-derived notes.
type MemoryDocument struct {
	Trusted, Untrusted string
	Revision           string
	Bytes, Lines       int
	MaxBytes, MaxLines int
}

type MemoryChangeKind string

const (
	MemoryAppend  MemoryChangeKind = "append"
	MemoryReplace MemoryChangeKind = "replace"
	MemoryClear   MemoryChangeKind = "clear"
)

type MemoryChange struct {
	Kind             MemoryChangeKind
	Content          string
	ExpectedRevision string
}

type MemoryActions struct {
	Read   func(context.Context) (MemoryDocument, error)
	Change func(context.Context, MemoryChange) (MemoryDocument, error)
}

type memoryReadMsg struct {
	intent string
	doc    MemoryDocument
	err    error
}

type memoryChangedMsg struct {
	kind MemoryChangeKind
	doc  MemoryDocument
	err  error
}

type memoryEditor struct {
	mode     MemoryChangeKind
	revision string
	textarea textarea.Model
}

func manageMemory(m chatModel, args string) (chatModel, tea.Cmd) {
	action, rest, _ := strings.Cut(strings.TrimSpace(args), " ")
	if action == "" {
		if m.actions.Memory.Read == nil || m.actions.Memory.Change == nil {
			return m.openCommandPanel(commandPanel{
				kind: commandPanelMemory, title: "memory", lines: []string{"memory management is unavailable"},
			}), nil
		}
		options := []commandPanelOption{
			{section: "Saved memory", label: "Edit", detail: "Edit your trusted notes", value: "edit"},
			{section: "Saved memory", label: "Clear", detail: "Remove your trusted notes", value: "clear", status: ReportWarning},
		}
		return m.openCommandPanel(commandPanel{kind: commandPanelMemory, title: "memory", options: options}), nil
	}
	if m.actions.Memory.Read == nil || m.actions.Memory.Change == nil {
		m.transcript.addNotice("○ Saved memory is unavailable in this session.")
		return m, nil
	}
	switch action {
	case "add":
		if text := strings.TrimSpace(rest); text != "" {
			return m.changeMemory(MemoryChange{Kind: MemoryAppend, Content: text})
		}
		return showMemoryUsage(m)
	case "edit":
		if strings.TrimSpace(rest) != "" {
			return showMemoryUsage(m)
		}
		return m.requestMemory("edit")
	case "clear":
		if strings.TrimSpace(rest) != "" {
			return showMemoryUsage(m)
		}
		return m.requestMemory("clear")
	default:
		return showMemoryUsage(m)
	}
}

func showMemoryUsage(m chatModel) (chatModel, tea.Cmd) {
	m.transcript.addNotice("usage: /memory [add <text>|edit|clear]")
	return m, nil
}

func (m chatModel) requestMemory(intent string) (chatModel, tea.Cmd) {
	read, ctx := m.actions.Memory.Read, m.ctx
	m = m.openCommandPanel(commandPanel{
		kind: commandPanelMemory, title: "memory", lines: []string{"… loading saved memory"},
	})
	return m, func() tea.Msg {
		doc, err := read(ctx)
		return memoryReadMsg{intent: intent, doc: doc, err: err}
	}
}

func (m chatModel) changeMemory(change MemoryChange) (chatModel, tea.Cmd) {
	apply, ctx := m.actions.Memory.Change, m.ctx
	return m, func() tea.Msg {
		doc, err := apply(ctx, change)
		return memoryChangedMsg{kind: change.Kind, doc: doc, err: err}
	}
}

func (m chatModel) finishMemoryRead(msg memoryReadMsg) (tea.Model, tea.Cmd) {
	if m.commandPanel == nil || m.commandPanel.kind != commandPanelMemory {
		return m, nil
	}
	if msg.err != nil {
		return m.openCommandPanel(commandPanel{
			kind:  commandPanelMemory,
			title: "memory",
			lines: []string{"✗ Could not read saved memory", "  " + cutFirstLine(msg.err.Error())},
		}), nil
	}
	m.commandPanel = nil
	switch msg.intent {
	case "edit":
		var cmd tea.Cmd
		m, cmd = m.openMemoryEditor(MemoryReplace, msg.doc)
		return m, cmd
	case "clear":
		revision := msg.doc.Revision
		m.confirmation = &confirmation{
			title: "Clear trusted memory? Untrusted-derived notes are preserved.",
			accept: func(ctx context.Context) tea.Msg {
				doc, err := m.actions.Memory.Change(ctx, MemoryChange{Kind: MemoryClear, ExpectedRevision: revision})
				return memoryChangedMsg{kind: MemoryClear, doc: doc, err: err}
			},
		}
	}
	m.refreshTranscript()
	m.viewport.GotoBottom()
	return m.muteDictationForModal()
}

func (m chatModel) openMemoryEditor(mode MemoryChangeKind, doc MemoryDocument) (chatModel, tea.Cmd) {
	editor := textarea.New()
	editor.Prompt = "  "
	editor.Placeholder = "Type trusted notes here…"
	editor.ShowLineNumbers = false
	editor.CharLimit = max(doc.MaxBytes, 8<<10)
	editor.MaxContentHeight = max(doc.MaxLines, 200)
	editor.SetValue(doc.Trusted)
	editor.SetWidth(max(m.width-4, 1))
	editor.SetHeight(max(m.inlinePanelHeight()-3, 3))
	editor.SetVirtualCursor(false)
	styles := editor.Styles()
	styles.Focused.Prompt = m.theme.assistantLabel
	styles.Focused.Placeholder = m.theme.dim
	styles.Cursor.Color = m.theme.cursor
	editor.SetStyles(styles)
	editor.CursorEnd()
	focus := editor.Focus()
	m.memoryEditor = &memoryEditor{mode: mode, revision: doc.Revision, textarea: editor}
	m.fitViewport()
	m.memoryEditor.textarea.SetHeight(max(m.inlinePanelHeight()-3, 3))
	m, mute := m.muteDictationForModal()
	return m, tea.Batch(focus, mute)
}

func (m chatModel) handleMemoryEditorKey(key tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch key.String() {
	case "esc":
		m.memoryEditor = nil
		m.input.Reset()
		m.fitViewport()
		return m, nil
	case "ctrl+s":
		editor := m.memoryEditor
		m.memoryEditor = nil
		m.fitViewport()
		return m.changeMemory(MemoryChange{
			Kind: editor.mode, Content: editor.textarea.Value(), ExpectedRevision: editor.revision,
		})
	}
	var cmd tea.Cmd
	editor := *m.memoryEditor
	editor.textarea, cmd = editor.textarea.Update(key)
	m.memoryEditor = &editor
	return m, cmd
}

func (m chatModel) finishMemoryChange(msg memoryChangedMsg) (tea.Model, tea.Cmd) {
	if msg.err != nil {
		m.transcript.addNotice("✗ Could not update saved memory")
		m.transcript.addNotice("  " + cutFirstLine(msg.err.Error()))
	} else {
		verb := map[MemoryChangeKind]string{MemoryAppend: "added", MemoryReplace: "saved", MemoryClear: "cleared"}[msg.kind]
		m.transcript.addNotice("✓ Saved memory " + verb)
		m.transcript.addNotice(fmt.Sprintf("  Using %d of %d bytes · %d of %d lines",
			msg.doc.Bytes, msg.doc.MaxBytes, msg.doc.Lines, msg.doc.MaxLines))
	}
	m.refreshTranscript()
	m.viewport.GotoBottom()
	return m, nil
}

func renderMemoryEditor(editor memoryEditor, width, height int, theme terminalTheme) string {
	title := "/memory add"
	if editor.mode == MemoryReplace {
		title = "/memory edit"
	}
	lines := []string{
		truncateWithMargin(theme.assistantLabel.Render(title+" · trusted notes"), width),
		truncateWithMargin(theme.dim.Render("ctrl+s save · esc cancel · untrusted-derived notes are preserved"), width),
		editor.textarea.View(),
	}
	return strings.Join(lines, "\n")
}
