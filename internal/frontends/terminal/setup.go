package terminal

import (
	"context"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
)

// SetupActions are the saves the setup wizard makes. Each step saves as the user leaves it,
// so quitting part-way keeps every step already finished.
type SetupActions struct {
	DescribeProviders func() []ProviderSummary
	// DetectLocalServers looks for model servers on this machine; it waits on the network, so it runs off the UI thread.
	DetectLocalServers func() []LocalServerSummary
	// SaveProvider makes profile the active one, storing apiKey first when it is not empty. A detected
	// local server's profile is created or updated first.
	SaveProvider func(profile, apiKey string) error
	// TestConnection sends profile's model one tiny request with one tool and reports one line per
	// check. It costs a model call, so the wizard runs it only when the user asks.
	TestConnection  func(profile string) []string
	SaveVoiceOutput func(isEnabled bool) error
	ListMCPServers  func() []string
	// DescribeSecurity says what the named security profile lets travis do, one row per line.
	DescribeSecurity    func(profile string) []string
	SaveSecurityProfile func(profile string) error
	// RecordProgress remembers the step the wizard moved to, so a later run can resume there.
	RecordProgress func(step string)
}

// LocalServerSummary is a model server the setup wizard looked for on this machine. A detected one
// with a model stands in for the configured profile of the same name.
type LocalServerSummary struct {
	Profile string
	// Address is where travis looked, such as 127.0.0.1:11434.
	Address string
	// Model is empty when the server was not detected or offers no model; then it cannot be chosen.
	Model string
	// ContextLength is Model's context window in tokens; 0 means the server did not say.
	ContextLength int
	IsDetected    bool
}

type localServersDetectedMsg struct{ servers []LocalServerSummary }

type connectionTestedMsg struct {
	profile string
	checks  []string
}

// setupProviderRow is one choice in the provider step: a configured profile or a local server.
type setupProviderRow struct {
	name, model, status string
	isChoosable         bool
	isKeyNeeded         bool
}

type SetupOptions struct {
	Input  io.Reader
	Output io.Writer
	// ConfigFile is where the answers are saved, shown so the user knows what changed.
	ConfigFile           string
	IsVoiceOutputEnabled bool
	// SecurityProfile is the configured profile, chosen until the user moves the cursor.
	SecurityProfile string
	// StartStep is the step to open at, as RecordProgress named it; an unknown name opens at the first.
	StartStep string
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

// setupSecurityProfiles leaves out yolo, which needs an RFC amending D-22 and D-27 first.
var setupSecurityProfiles = []string{"paranoid", "default", "dev"}

const (
	setupLegend         = "↑↓ choose · ⏎ continue · tab skip · ctrl+c quit"
	setupProviderLegend = "↑↓ choose · ⏎ continue · t test connection · tab skip · ctrl+c quit"
)

type setupModel struct {
	actions SetupActions
	opts    SetupOptions
	step    setupStep

	providers    []ProviderSummary
	localServers []LocalServerSummary
	// hasLookedForLocalServers is false until DetectLocalServers answers.
	hasLookedForLocalServers bool
	cursor                   int
	isEnteringKey            bool
	keyInput                 textinput.Model
	isVoiceOn                bool
	profileCursor            int
	// connectionTest is the last test's header line and one line per check.
	connectionTest []string
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
	profileCursor := slices.Index(setupSecurityProfiles, opts.SecurityProfile)
	if profileCursor < 0 {
		profileCursor = slices.Index(setupSecurityProfiles, "default")
	}
	step := setupStep(max(slices.Index(setupStepNames, opts.StartStep), 0))
	return setupModel{actions: actions, opts: opts, step: step, keyInput: keyInput, isVoiceOn: opts.IsVoiceOutputEnabled, profileCursor: profileCursor}
}

func (m setupModel) Init() tea.Cmd {
	describe, detect := m.actions.DescribeProviders, m.actions.DetectLocalServers
	return tea.Batch(
		func() tea.Msg { return providersDescribedMsg{providers: describe()} },
		func() tea.Msg { return localServersDetectedMsg{servers: detect()} },
	)
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
	case localServersDetectedMsg:
		m.localServers, m.hasLookedForLocalServers = msg.servers, true
	case connectionTestedMsg:
		m.connectionTest = append([]string{"test · " + msg.profile}, msg.checks...)
	case tea.PasteMsg:
		if m.isEnteringKey {
			var cmd tea.Cmd
			m.keyInput, cmd = m.keyInput.Update(msg)
			return m, cmd
		}
	case tea.KeyPressMsg:
		next, cmd := m.handleKey(msg)
		if step := next.(setupModel).step; step != m.step {
			m.actions.RecordProgress(setupStepNames[step])
		}
		return next, cmd
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
	case setupMCP:
		if key.String() == "enter" {
			m.step++
		}
	case setupSecurity:
		switch key.String() {
		case "up", "k":
			m.profileCursor = max(m.profileCursor-1, 0)
		case "down", "j":
			m.profileCursor = min(m.profileCursor+1, len(setupSecurityProfiles)-1)
		case "enter":
			chosen := setupSecurityProfiles[m.profileCursor]
			return m.saveAndAdvance(func() error { return m.actions.SaveSecurityProfile(chosen) })
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
		m.cursor = max(min(m.cursor+1, len(m.listProviderRows())-1), 0)
	case "enter":
		rows := m.listProviderRows()
		if len(rows) == 0 {
			return m, nil
		}
		chosen := rows[m.cursor]
		switch {
		case !chosen.isChoosable:
			m.notice = "✗ " + chosen.name + ": " + chosen.status
			return m, nil
		case chosen.isKeyNeeded:
			m.isEnteringKey = true
			m.keyInput.Reset()
			return m, m.keyInput.Focus()
		}
		return m.saveAndAdvance(func() error { return m.actions.SaveProvider(chosen.name, "") })
	case "t":
		rows := m.listProviderRows()
		if len(rows) == 0 || !rows[m.cursor].isChoosable {
			return m, nil
		}
		profile, testConnection := rows[m.cursor].name, m.actions.TestConnection
		m.connectionTest = []string{"test · " + profile, "testing…"}
		return m, func() tea.Msg { return connectionTestedMsg{profile: profile, checks: testConnection(profile)} }
	}
	return m, nil
}

// listProviderRows lists configured profiles, then local servers. A detected server with a model
// replaces the profile of the same name in place, so the cursor keeps pointing at the same row.
func (m setupModel) listProviderRows() []setupProviderRow {
	rows := make([]setupProviderRow, 0, len(m.providers)+len(m.localServers))
	for _, provider := range m.providers {
		rows = append(rows, setupProviderRow{name: provider.Name, model: provider.Model, status: reportKeyState(provider),
			isChoosable: true, isKeyNeeded: provider.IsKeyRequired && !provider.HasKey})
	}
	for _, server := range m.localServers {
		row := setupProviderRow{name: server.Profile, model: server.Model, status: describeLocalServer(server), isChoosable: server.Model != ""}
		configured := slices.IndexFunc(rows, func(r setupProviderRow) bool { return r.name == server.Profile })
		switch {
		case configured < 0:
			rows = append(rows, row)
		case row.isChoosable:
			rows[configured] = row
		}
	}
	return rows
}

func describeLocalServer(server LocalServerSummary) string {
	switch {
	case !server.IsDetected:
		return "not detected at " + server.Address
	case server.Model == "":
		return "detected at " + server.Address + ", but it has no model yet"
	case server.ContextLength > 0:
		return fmt.Sprintf("detected at %s · context %d tokens", server.Address, server.ContextLength)
	default:
		return "detected at " + server.Address
	}
}

// handleKeyEntryKey collects the key masked; it is handed to SaveProvider and never rendered.
func (m setupModel) handleKeyEntryKey(key tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch key.String() {
	case "esc":
		m.leaveKeyEntry()
		return m, nil
	case "enter":
		profile, apiKey := m.listProviderRows()[m.cursor].name, m.keyInput.Value()
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
	legend := setupLegend
	if m.step == setupProvider {
		legend = setupProviderLegend
	}
	lines = append(lines, "", " "+dimStyle.Render(legend))
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
		rows := m.listProviderRows()
		for i, provider := range rows {
			row := fmt.Sprintf("   %-10s %-24s %s", provider.name, provider.model, provider.status)
			if i == m.cursor {
				row = selectedRowStyle.Render("▸" + row[1:])
			}
			lines = append(lines, row)
		}
		if !m.hasLookedForLocalServers {
			lines = append(lines, dimStyle.Render("   looking for Ollama and llama.cpp on this machine…"))
		}
		if len(m.connectionTest) > 0 {
			lines = append(append(lines, "", " "+m.connectionTest[0]), indentLines(m.connectionTest[1:])...)
		}
		if m.isEnteringKey {
			lines = append(lines, "", " "+rows[m.cursor].name+" needs an API key; it is saved to the OS credential store, never to the config file.",
				" Paste it and press enter · enter on empty continues without one · esc goes back", m.keyInput.View())
		}
		return lines
	case setupVoice:
		choice := "[ off ]  on"
		if m.isVoiceOn {
			choice = " off  [ on ]"
		}
		return []string{" Speak replies aloud?", "", "   " + choice, "",
			dimStyle.Render(" Push-to-talk dictation also needs the voice module: make install-voice (docs/DEV-SETUP.md).")}
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
		lines := []string{" Choose what travis may do", ""}
		for i, profile := range setupSecurityProfiles {
			row := "   " + profile
			if i == m.profileCursor {
				row = selectedRowStyle.Render("▸" + row[1:])
			}
			lines = append(lines, row)
		}
		return append(append(lines, ""), indentLines(m.actions.DescribeSecurity(setupSecurityProfiles[m.profileCursor]))...)
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
