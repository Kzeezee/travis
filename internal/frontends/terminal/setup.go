package terminal

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
)

// SetupActions are the saves the setup wizard makes. Each step saves as the user leaves it,
// so quitting part-way keeps every step already finished.
type SetupActions struct {
	DescribeProviders func() []ProviderSummary
	// SaveProvider makes profile the active one, storing apiKey first when it is not empty.
	SaveProvider     func(profile, apiKey string) error
	SaveVoiceOutput  func(isEnabled bool) error
	ListMCPServers   func() []string
	DescribeSecurity func() []string
}

type SetupOptions struct {
	Input  io.Reader
	Output io.Writer
	// ConfigFile is where the answers are saved, shown so the user knows what changed.
	ConfigFile           string
	IsVoiceOutputEnabled bool
}

type setupStep int

const (
	setupProvider setupStep = iota
	setupVoice
	setupMCP
	setupSecurity
	setupDone
)

var setupStepNames = []string{"provider", "voice", "mcp", "security", "done"}

const setupLegend = "↑↓ choose · ⏎ continue · tab skip · ctrl+c quit"

type setupModel struct {
	actions SetupActions
	opts    SetupOptions
	step    setupStep

	providers     []ProviderSummary
	cursor        int
	isEnteringKey bool
	keyInput      textinput.Model
	isVoiceOn     bool
	// notice is the last save's failure, shown until the user acts again.
	notice     string
	isFinished bool
}

// RunSetup runs the first-run wizard (DESIGN.md §7.1). It reports whether the user reached the
// end; false means they quit, though every step they finished is already saved.
func RunSetup(ctx context.Context, actions SetupActions, opts SetupOptions) (bool, error) {
	if !isTerminal(opts.Input) || !isTerminal(opts.Output) {
		return false, ErrNotInteractive
	}
	program := tea.NewProgram(buildSetupModel(actions, opts), tea.WithInput(opts.Input), tea.WithOutput(opts.Output), tea.WithContext(ctx))
	final, err := program.Run()
	if errors.Is(err, tea.ErrInterrupted) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return final.(setupModel).isFinished, nil
}

func buildSetupModel(actions SetupActions, opts SetupOptions) setupModel {
	keyInput := textinput.New()
	keyInput.EchoMode = textinput.EchoPassword
	keyInput.Prompt = " key › "
	return setupModel{actions: actions, opts: opts, keyInput: keyInput, isVoiceOn: opts.IsVoiceOutputEnabled}
}

func (m setupModel) Init() tea.Cmd {
	describe := m.actions.DescribeProviders
	return func() tea.Msg { return providersDescribedMsg{providers: describe()} }
}

func (m setupModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case providersDescribedMsg:
		m.providers = msg.providers
		for i, provider := range m.providers {
			if provider.IsActive {
				m.cursor = i
			}
		}
	case tea.PasteMsg:
		if m.isEnteringKey {
			var cmd tea.Cmd
			m.keyInput, cmd = m.keyInput.Update(msg)
			return m, cmd
		}
	case tea.KeyPressMsg:
		return m.handleKey(msg)
	}
	return m, nil
}

func (m setupModel) handleKey(key tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch key.String() {
	case "ctrl+c":
		return m, tea.Quit
	case "tab":
		if m.step < setupDone {
			m.leaveKeyEntry()
			m.step++
			m.notice = ""
		}
		return m, nil
	}
	if m.isEnteringKey {
		return m.handleKeyEntryKey(key)
	}
	switch m.step {
	case setupProvider:
		return m.handleProviderKey(key)
	case setupVoice:
		switch key.String() {
		case "left", "right", "up", "down", "space", "y", "n":
			m.isVoiceOn = key.String() == "y" || (key.String() != "n" && !m.isVoiceOn)
		case "enter":
			return m.saveAndAdvance(func() error { return m.actions.SaveVoiceOutput(m.isVoiceOn) })
		}
	case setupMCP, setupSecurity:
		if key.String() == "enter" {
			m.step++
		}
	case setupDone:
		if key.String() == "enter" {
			m.isFinished = true
			return m, tea.Quit
		}
	}
	return m, nil
}

func (m setupModel) handleProviderKey(key tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch key.String() {
	case "up", "k":
		m.cursor = max(m.cursor-1, 0)
	case "down", "j":
		m.cursor = min(m.cursor+1, len(m.providers)-1)
	case "enter":
		if len(m.providers) == 0 {
			return m, nil
		}
		chosen := m.providers[m.cursor]
		if chosen.IsKeyRequired && !chosen.HasKey {
			m.isEnteringKey = true
			m.keyInput.Reset()
			return m, m.keyInput.Focus()
		}
		return m.saveAndAdvance(func() error { return m.actions.SaveProvider(chosen.Name, "") })
	}
	return m, nil
}

// handleKeyEntryKey collects the key masked; it is handed to SaveProvider and never rendered.
func (m setupModel) handleKeyEntryKey(key tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch key.String() {
	case "esc":
		m.leaveKeyEntry()
		return m, nil
	case "enter":
		profile, apiKey := m.providers[m.cursor].Name, m.keyInput.Value()
		m.leaveKeyEntry()
		return m.saveAndAdvance(func() error { return m.actions.SaveProvider(profile, apiKey) })
	}
	var cmd tea.Cmd
	m.keyInput, cmd = m.keyInput.Update(key)
	return m, cmd
}

func (m *setupModel) leaveKeyEntry() {
	m.isEnteringKey = false
	m.keyInput.Reset()
	m.keyInput.Blur()
}

// saveAndAdvance stays on the step when the save fails, so the user can retry or skip it.
func (m setupModel) saveAndAdvance(save func() error) (tea.Model, tea.Cmd) {
	if err := save(); err != nil {
		m.notice = "✗ " + cutFirstLine(err.Error())
		return m, nil
	}
	m.notice = ""
	m.step++
	return m, nil
}

func (m setupModel) View() tea.View {
	lines := []string{
		fmt.Sprintf(" travis · setup%s step %d of %d", strings.Repeat(" ", 8), int(m.step)+1, len(setupStepNames)),
		" " + renderSetupProgress(m.step) + "   " + strings.Join(setupStepNames, " · "),
		"",
	}
	lines = append(lines, m.renderStep()...)
	if m.notice != "" {
		lines = append(lines, "", " "+errorStyle.Render(m.notice))
	}
	lines = append(lines, "", " "+dimStyle.Render(setupLegend))
	view := tea.NewView(strings.Join(lines, "\n"))
	view.AltScreen = true
	return view
}

func renderSetupProgress(current setupStep) string {
	marks := make([]string, len(setupStepNames))
	for i := range marks {
		marks[i] = "○"
		if setupStep(i) <= current {
			marks[i] = "●"
		}
	}
	return strings.Join(marks, "───")
}

func (m setupModel) renderStep() []string {
	switch m.step {
	case setupProvider:
		lines := []string{" Choose your model provider", ""}
		for i, provider := range m.providers {
			row := fmt.Sprintf("   %-10s %-24s %s", provider.Name, provider.Model, reportKeyState(provider))
			if i == m.cursor {
				row = selectedRowStyle.Render("▸" + row[1:])
			}
			lines = append(lines, row)
		}
		if m.isEnteringKey {
			lines = append(lines, "", " "+m.providers[m.cursor].Name+" needs an API key; it is saved to the OS credential store, never to the config file.",
				" Paste it and press enter · enter on empty continues without one · esc goes back", m.keyInput.View())
		}
		return lines
	case setupVoice:
		choice := "[ off ]  on"
		if m.isVoiceOn {
			choice = " off  [ on ]"
		}
		return []string{" Speak replies aloud?", "", "   " + choice, "",
			dimStyle.Render(" Push-to-talk dictation also needs travis-audio and a local whisper server (docs/DEV-SETUP.md).")}
	case setupMCP:
		lines := []string{" MCP servers give travis more tools.", ""}
		servers := m.actions.ListMCPServers()
		if len(servers) == 0 {
			lines = append(lines, "   none configured yet")
		}
		for _, name := range servers {
			lines = append(lines, "   "+name)
		}
		return append(lines, "", " Add one after setup, with its version pinned:",
			"   travis mcp add <name> --allow-net -- npx -y <package>@<version>",
			dimStyle.Render(" travis shows where the package comes from and asks before installing it."))
	case setupSecurity:
		return append([]string{" What travis can and cannot do", ""}, indentLines(m.actions.DescribeSecurity())...)
	case setupDone:
		return []string{" Setup is saved to " + m.opts.ConfigFile, "",
			" Change it any time: /provider in chat, travis mcp add, or travis init again.",
			" Press enter to start chatting."}
	}
	return nil
}

func indentLines(lines []string) []string {
	indented := make([]string, len(lines))
	for i, line := range lines {
		indented[i] = "   " + line
	}
	return indented
}
