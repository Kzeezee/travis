package terminal

import (
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
)

// ProviderSwitch is what switching provider profile produced: plain strings only, so the
// terminal never imports config or provider packages to render it.
type ProviderSwitch struct {
	Model string
	// ContextLength is the model's context window in tokens; 0 means unknown.
	ContextLength int
	// IsKeyNeeded means nothing switched: the profile needs a key the user has not given yet.
	IsKeyNeeded bool
	// Warning is something the user should know although the switch succeeded.
	Warning string
}

// ProviderSummary is one provider profile as /provider lists it.
type ProviderSummary struct {
	Name, Model string
	IsActive    bool
	// IsKeyRequired is false for an endpoint on this machine; HasKey matters only when it is true.
	IsKeyRequired, HasKey bool
}

type providersDescribedMsg struct{ providers []ProviderSummary }

type providerSwitchedMsg struct {
	profile string
	result  ProviderSwitch
	err     error
}

const keyPromptNotice = " needs an API key. It will be saved in the OS credential store."

func (m chatModel) requestProviderSwitch(profile string) (chatModel, tea.Cmd) {
	if m.actions.SwitchProvider == nil {
		m.transcript.addNotice("○ Provider switching is unavailable in this session.")
		return m, nil
	}
	if profile == "" {
		return m.requestProviderDescriptions()
	}
	switchProvider := m.actions.SwitchProvider
	return m, func() tea.Msg {
		result, err := switchProvider(profile)
		return providerSwitchedMsg{profile: profile, result: result, err: err}
	}
}

func (m chatModel) requestProviderDescriptions() (chatModel, tea.Cmd) {
	if m.actions.DescribeProviders == nil {
		names := m.listProviderNames()
		options := make([]commandPanelOption, 0, len(names))
		for _, name := range names {
			options = append(options, commandPanelOption{label: name, value: name})
		}
		lines := []string(nil)
		if len(options) == 0 {
			lines = []string{"no provider profiles are available"}
		}
		return m.openCommandPanel(commandPanel{
			kind: commandPanelProvider, title: "provider", lines: lines, options: options,
		}), nil
	}
	describe := m.actions.DescribeProviders
	m = m.openCommandPanel(commandPanel{kind: commandPanelProvider, title: "provider", lines: []string{"loading providers…"}})
	return m, func() tea.Msg { return providersDescribedMsg{providers: describe()} }
}

func (m chatModel) showProviderDescriptions(msg providersDescribedMsg) (tea.Model, tea.Cmd) {
	if m.commandPanel == nil || m.commandPanel.kind != commandPanelProvider {
		return m, nil
	}
	options := make([]commandPanelOption, 0, len(msg.providers))
	for _, provider := range msg.providers {
		detail := provider.Model + " · " + reportKeyState(provider)
		status := ReportNeutral
		if provider.IsActive {
			detail += " · current"
			status = ReportCurrent
		} else if provider.IsKeyRequired && !provider.HasKey {
			status = ReportWarning
		}
		options = append(options, commandPanelOption{
			label: provider.Name, detail: detail, value: provider.Name, status: status,
		})
	}
	lines := []string(nil)
	if len(options) == 0 {
		lines = []string{"no provider profiles are available"}
	}
	m = m.openCommandPanel(commandPanel{
		kind: commandPanelProvider, title: "provider", lines: lines, options: options,
	})
	return m, nil
}

func reportKeyState(provider ProviderSummary) string {
	switch {
	case !provider.IsKeyRequired:
		return "Local · no key needed"
	case provider.HasKey:
		return "Key saved"
	default:
		return "Needs an API key"
	}
}

func (m chatModel) listProviderNames() []string {
	if m.actions.ListProviders == nil {
		return nil
	}
	return m.actions.ListProviders()
}

func (m chatModel) finishProviderSwitch(msg providerSwitchedMsg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd
	switch {
	case msg.err != nil:
		m.transcript.addNotice("✗ Could not change provider")
		m.transcript.addNotice("  " + cutFirstLine(msg.err.Error()))
	case msg.result.IsKeyNeeded:
		m.keyPromptProfile = msg.profile
		m.input.Reset()
		m.input.EchoMode = textinput.EchoPassword
		m.input.Prompt = " key › "
		m.transcript.addNotice(msg.profile + keyPromptNotice)
		m, cmd = m.muteDictationForModal()
	default:
		m.modelName = msg.result.Model
		m.contextLength = msg.result.ContextLength
		m.modelNames = nil
		m.transcript.addNotice("✓ Provider changed: " + msg.profile)
		m.transcript.addNotice("  Model: " + msg.result.Model)
		if msg.result.Warning != "" {
			m.transcript.addNotice("! Provider warning")
			m.transcript.addNotice("  " + msg.result.Warning)
		}
		if m.actions.ListModels != nil {
			cmd = listModels(m.ctx, m.actions.ListModels)
		}
	}
	m.refreshTranscript()
	m.viewport.GotoBottom()
	return m, cmd
}

// handleKeyPromptKey takes every key while a key is being entered; the key is never echoed,
// added to the transcript, or sent as a prompt.
func (m chatModel) handleKeyPromptKey(key tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch key.String() {
	case "esc", "ctrl+c":
		m.closeKeyPrompt()
		m.transcript.addNotice("Provider was not changed.")
		m.refreshTranscript()
		return m, nil
	case "enter":
		profile, apiKey := m.keyPromptProfile, m.input.Value()
		m.closeKeyPrompt()
		switchProvider := m.actions.SwitchProviderWithKey
		return m, func() tea.Msg {
			result, err := switchProvider(profile, apiKey)
			return providerSwitchedMsg{profile: profile, result: result, err: err}
		}
	}
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(key)
	return m, cmd
}

func (m *chatModel) closeKeyPrompt() {
	m.keyPromptProfile = ""
	m.input.Reset()
	m.input.EchoMode = textinput.EchoNormal
	m.input.Prompt = " › "
}
