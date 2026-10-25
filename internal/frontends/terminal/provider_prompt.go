package terminal

import (
	"strings"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
)

// ProviderSwitch is what switching provider profile produced: plain strings only, so the
// terminal never imports config or provider packages to render it.
type ProviderSwitch struct {
	Model string
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

const keyPromptNotice = " needs an API key: paste it and press enter to save it to the OS credential store · enter on empty switches without one · esc cancels"

func (m chatModel) requestProviderSwitch(profile string) (chatModel, tea.Cmd) {
	if m.actions.SwitchProvider == nil {
		m.transcript.addNotice("switching provider is not available in this session")
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
		m.transcript.addNotice("provider profiles: " + strings.Join(m.listProviderNames(), ", ") + " · /provider <name> switches")
		return m, nil
	}
	describe := m.actions.DescribeProviders
	return m, func() tea.Msg { return providersDescribedMsg{providers: describe()} }
}

func (m chatModel) showProviderDescriptions(msg providersDescribedMsg) (tea.Model, tea.Cmd) {
	for _, provider := range msg.providers {
		marker := "  "
		if provider.IsActive {
			marker = "▸ "
		}
		m.transcript.addNotice(marker + provider.Name + " · " + provider.Model + " · " + reportKeyState(provider))
	}
	m.transcript.addNotice("/provider <name> switches")
	m.refreshTranscript()
	m.viewport.GotoBottom()
	return m, nil
}

func reportKeyState(provider ProviderSummary) string {
	switch {
	case !provider.IsKeyRequired:
		return "local, no key needed"
	case provider.HasKey:
		return "key ✓"
	default:
		return "no key yet"
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
		m.transcript.addNotice("✗ provider: " + cutFirstLine(msg.err.Error()))
	case msg.result.IsKeyNeeded:
		m.keyPromptProfile = msg.profile
		m.input.Reset()
		m.input.EchoMode = textinput.EchoPassword
		m.input.Prompt = " key › "
		m.transcript.addNotice(msg.profile + keyPromptNotice)
		m, cmd = m.muteDictationForModal()
	default:
		m.modelName = msg.result.Model
		m.modelNames = nil
		m.transcript.addNotice("provider → " + msg.profile + " (" + msg.result.Model + ")")
		if msg.result.Warning != "" {
			m.transcript.addNotice("⚠ provider: " + msg.result.Warning)
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
		m.transcript.addNotice("provider unchanged")
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
