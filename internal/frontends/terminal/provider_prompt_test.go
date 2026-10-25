package terminal

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

// providerActions is a fake switch: "cloud" needs a key until one is given, "local" never does.
type providerActions struct {
	recordingActions
	givenKeys []string
}

func (p *providerActions) buildChatActions() ChatActions {
	actions := p.recordingActions.buildChatActions()
	actions.ListProviders = func() []string { return []string{"cloud", "local"} }
	actions.SwitchProvider = func(profile string) (ProviderSwitch, error) {
		if profile == "cloud" {
			return ProviderSwitch{IsKeyNeeded: true}, nil
		}
		return ProviderSwitch{Model: profile + "-model"}, nil
	}
	actions.SwitchProviderWithKey = func(profile, apiKey string) (ProviderSwitch, error) {
		p.givenKeys = append(p.givenKeys, apiKey)
		return ProviderSwitch{Model: profile + "-model"}, nil
	}
	return actions
}

// applyCmd feeds the message a command produced back into the model, as the Bubble Tea runtime would.
func applyCmd(t *testing.T, m chatModel, cmd tea.Cmd) chatModel {
	t.Helper()
	if cmd == nil {
		t.Fatal("got no command, want one to run")
	}
	m, _ = updateModel(m, cmd())
	return m
}

func TestProviderSwitchesStraightAwayWhenNoKeyIsNeeded(t *testing.T) {
	actions := &providerActions{}
	m := buildTestModel(actions.buildChatActions())

	m, cmd := pressKeys(m, append(typedKeys("/provider local"), enterKey)...)
	m = applyCmd(t, m, cmd)

	if view := renderPlainView(m); !strings.Contains(view, "provider → local (local-model)") || !strings.Contains(view, " local-model · idle") {
		t.Fatalf("got view\n%s\nwant the switch notice and the new model in the status bar", view)
	}
}

func TestProviderAsksForAMaskedKeyThenSwitches(t *testing.T) {
	actions := &providerActions{}
	m := buildTestModel(actions.buildChatActions())

	m, cmd := pressKeys(m, append(typedKeys("/provider cloud"), enterKey)...)
	m = applyCmd(t, m, cmd)
	m, _ = pressKeys(m, typedKeys("sk-secret-value")...)

	if view := renderPlainView(m); strings.Contains(view, "sk-secret-value") || !strings.Contains(view, "cloud needs an API key") {
		t.Fatalf("got view\n%s\nwant the key prompt with the typed key masked", view)
	}
	m, cmd = pressKeys(m, enterKey)
	m = applyCmd(t, m, cmd)

	if len(actions.givenKeys) != 1 || actions.givenKeys[0] != "sk-secret-value" || len(actions.prompts) != 0 {
		t.Fatalf("got keys %q and prompts %q, want the key handed over and never sent as a prompt", actions.givenKeys, actions.prompts)
	}
	if view := renderPlainView(m); strings.Contains(view, "sk-secret-value") || !strings.Contains(view, "provider → cloud (cloud-model)") {
		t.Fatalf("got view\n%s\nwant the switch notice and no key anywhere", view)
	}
	if m.input.EchoMode != 0 || m.keyPromptProfile != "" {
		t.Fatal("the input is still masked after the key was given")
	}
}

func TestProviderKeyPromptCancelsOnEscape(t *testing.T) {
	actions := &providerActions{}
	m := buildTestModel(actions.buildChatActions())

	m, cmd := pressKeys(m, append(typedKeys("/provider cloud"), enterKey)...)
	m = applyCmd(t, m, cmd)
	m, cmd = pressKeys(m, append(typedKeys("sk-half"), escKey)...)

	if cmd != nil || len(actions.givenKeys) != 0 || m.input.Value() != "" || !strings.Contains(renderPlainView(m), "provider unchanged") {
		t.Fatalf("got keys %q and input %q, want nothing handed over and the input cleared", actions.givenKeys, m.input.Value())
	}
}

func TestProviderWithoutANameDescribesEachProfile(t *testing.T) {
	actions := (&providerActions{}).buildChatActions()
	actions.DescribeProviders = func() []ProviderSummary {
		return []ProviderSummary{
			{Name: "cloud", Model: "gpt", IsActive: true, IsKeyRequired: true, HasKey: true},
			{Name: "gemini", Model: "flash", IsKeyRequired: true},
			{Name: "local", Model: "tiny"},
		}
	}
	m := buildTestModel(actions)

	m, cmd := pressKeys(m, append(typedKeys("/provider"), enterKey)...)
	m = applyCmd(t, m, cmd)

	view := renderPlainView(m)
	for _, want := range []string{"▸ cloud · gpt · key ✓", "gemini · flash · no key yet", "local · tiny · local, no key needed"} {
		if !strings.Contains(view, want) {
			t.Fatalf("got view\n%s\nwant %q", view, want)
		}
	}
}

func TestMCPListsEachServerWithItsStateAndTools(t *testing.T) {
	actions := (&providerActions{}).buildChatActions()
	actions.ListMCPServers = func() []MCPServerSummary {
		return []MCPServerSummary{
			{Name: "github", State: "running", ToolCount: 12},
			{Name: "notes", State: "failed to start", Reason: "denied by policy\nsecond line"},
		}
	}
	m := buildTestModel(actions)

	m, _ = pressKeys(m, append(typedKeys("/mcp"), enterKey)...)

	view := renderPlainView(m)
	if !strings.Contains(view, "github · running · 12 tools") || !strings.Contains(view, "notes · failed to start · 0 tools · denied by policy") || strings.Contains(view, "second line") {
		t.Fatalf("got view\n%s\nwant each server, its state, its tools and the first line of its reason", view)
	}
}

func TestMCPWithNoServersSaysHowToAddOne(t *testing.T) {
	m := buildTestModel((&providerActions{}).buildChatActions())

	m, _ = pressKeys(m, append(typedKeys("/mcp"), enterKey)...)

	if view := renderPlainView(m); !strings.Contains(view, "no servers configured") {
		t.Fatalf("got view\n%s\nwant the empty-state hint", view)
	}
}

func TestModelWithoutANameCountsTheEndpointsModels(t *testing.T) {
	m := buildTestModel((&providerActions{}).buildChatActions())
	m, _ = updateModel(m, modelsListedMsg{names: []string{"a", "b", "c"}})

	m, _ = pressKeys(m, append(typedKeys("/model"), enterKey)...)

	if view := renderPlainView(m); !strings.Contains(view, "model: test-model · 3 available") {
		t.Fatalf("got view\n%s\nwant the model count", view)
	}
}
