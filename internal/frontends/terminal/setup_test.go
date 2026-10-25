package terminal

import (
	"errors"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

type setupRecorder struct {
	savedProfile, savedKey string
	voice                  *bool
	saveErr                error
}

func (r *setupRecorder) buildSetupModel() setupModel {
	m := buildSetupModel(SetupActions{
		DescribeProviders: func() []ProviderSummary {
			return []ProviderSummary{
				{Name: "cloud", Model: "gpt", IsActive: true, IsKeyRequired: true},
				{Name: "local", Model: "tiny"},
			}
		},
		SaveProvider: func(profile, apiKey string) error {
			r.savedProfile, r.savedKey = profile, apiKey
			return r.saveErr
		},
		SaveVoiceOutput:  func(isEnabled bool) error { r.voice = &isEnabled; return nil },
		ListMCPServers:   func() []string { return nil },
		DescribeSecurity: func() []string { return []string{"can read and write   /work"} },
	}, SetupOptions{ConfigFile: "/config/config.toml"})
	next, _ := m.Update(m.Init()())
	return next.(setupModel)
}

func pressSetupKeys(m setupModel, msgs ...tea.Msg) (setupModel, tea.Cmd) {
	var cmd tea.Cmd
	for _, msg := range msgs {
		var next tea.Model
		next, cmd = m.Update(msg)
		m = next.(setupModel)
	}
	return m, cmd
}

func renderSetup(m setupModel) string {
	return ansi.Strip(m.View().Content)
}

func TestSetupAsksForAMissingKeyMaskedAndSavesIt(t *testing.T) {
	recorder := &setupRecorder{}
	m := recorder.buildSetupModel()

	m, _ = pressSetupKeys(m, enterKey)
	m, _ = pressSetupKeys(m, typedKeys("sk-setup-secret")...)
	if view := renderSetup(m); strings.Contains(view, "sk-setup-secret") || !strings.Contains(view, "cloud needs an API key") {
		t.Fatalf("got view\n%s\nwant the key prompt with the key masked", view)
	}
	m, _ = pressSetupKeys(m, enterKey)

	if recorder.savedProfile != "cloud" || recorder.savedKey != "sk-setup-secret" || m.step != setupVoice {
		t.Fatalf("saved %q/%q at step %d, want cloud with its key and the voice step next", recorder.savedProfile, recorder.savedKey, m.step)
	}
}

func TestSetupWalksEveryStepToTheEnd(t *testing.T) {
	recorder := &setupRecorder{}
	m := recorder.buildSetupModel()

	m, _ = pressSetupKeys(m, tea.KeyPressMsg{Code: tea.KeyDown}, enterKey) // local needs no key
	m, _ = pressSetupKeys(m, typedKeys("y")...)
	m, _ = pressSetupKeys(m, enterKey) // voice on
	if !strings.Contains(renderSetup(m), "none configured yet") {
		t.Fatalf("got view\n%s\nwant the MCP step", renderSetup(m))
	}
	m, _ = pressSetupKeys(m, enterKey)
	if !strings.Contains(renderSetup(m), "can read and write   /work") {
		t.Fatalf("got view\n%s\nwant the security summary", renderSetup(m))
	}
	m, cmd := pressSetupKeys(m, enterKey, enterKey)

	if recorder.savedProfile != "local" || recorder.savedKey != "" || recorder.voice == nil || !*recorder.voice {
		t.Fatalf("saved %q/%q and voice %v, want local with no key and voice on", recorder.savedProfile, recorder.savedKey, recorder.voice)
	}
	if !m.isFinished || cmd == nil {
		t.Fatal("setup did not finish on the last enter")
	}
}

func TestSetupSkipsAStepWithoutSavingAndStaysOnAFailedSave(t *testing.T) {
	recorder := &setupRecorder{saveErr: errors.New("keychain locked")}
	m := recorder.buildSetupModel()

	m, _ = pressSetupKeys(m, tea.KeyPressMsg{Code: tea.KeyDown}, enterKey)
	if m.step != setupProvider || !strings.Contains(renderSetup(m), "✗ keychain locked") {
		t.Fatalf("got step %d and view\n%s\nwant to stay on the provider step with the error", m.step, renderSetup(m))
	}
	m, _ = pressSetupKeys(m, tea.KeyPressMsg{Code: tea.KeyTab}, tea.KeyPressMsg{Code: tea.KeyTab})
	if m.step != setupMCP || recorder.voice != nil {
		t.Fatalf("got step %d and voice %v, want two skipped steps with nothing saved", m.step, recorder.voice)
	}
}

func TestSetupQuitsWithoutFinishing(t *testing.T) {
	m := (&setupRecorder{}).buildSetupModel()

	m, cmd := pressSetupKeys(m, ctrlKey('c'))

	if m.isFinished || !isQuitCmd(cmd) {
		t.Fatal("ctrl+c did not quit unfinished")
	}
}
