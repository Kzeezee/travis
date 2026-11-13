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
	savedSecurityProfile   string
	voice                  *bool
	saveErr                error
	recordedSteps          []string
	localServers           []LocalServerSummary
	testedProfiles         []string
	chosenModels           []string
}

func (r *setupRecorder) buildSetupModel() setupModel {
	return r.buildSetupModelAt("")
}

func (r *setupRecorder) buildSetupModelAt(startStep string) setupModel {
	m := buildSetupModel(SetupActions{
		DescribeProviders: func() []ProviderSummary {
			return []ProviderSummary{
				{Name: "cloud", Model: "gpt", IsActive: true, IsKeyRequired: true},
				{Name: "local", Model: "tiny"},
			}
		},
		DetectLocalServers: func() []LocalServerSummary { return r.localServers },
		ChooseLocalModel: func(profile, model string) int {
			r.chosenModels = append(r.chosenModels, profile+"/"+model)
			return len(model) * 1000
		},
		SaveProvider: func(profile, apiKey string) error {
			r.savedProfile, r.savedKey = profile, apiKey
			return r.saveErr
		},
		TestConnection: func(profile string) []string {
			r.testedProfiles = append(r.testedProfiles, profile)
			return []string{"✓ reachable (34 ms)", "✓ streaming", "✗ tool call: answered without calling it"}
		},
		SaveVoiceOutput:     func(isEnabled bool) error { r.voice = &isEnabled; return nil },
		ListMCPServers:      func() []string { return nil },
		DescribeSecurity:    func(profile string) []string { return []string{profile + " can read   /work"} },
		SaveSecurityProfile: func(profile string) error { r.savedSecurityProfile = profile; return nil },
		RecordProgress:      func(step string) { r.recordedSteps = append(r.recordedSteps, step) },
	}, SetupOptions{ConfigFile: "/config/config.toml", SecurityProfile: "default", StartStep: startStep})
	for _, cmd := range m.Init()().(tea.BatchMsg) {
		next, _ := m.Update(cmd())
		m = next.(setupModel)
	}
	return m
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
	if !strings.Contains(renderSetup(m), "default can read   /work") {
		t.Fatalf("got view\n%s\nwant the configured profile's security summary", renderSetup(m))
	}
	m, cmd := pressSetupKeys(m, enterKey, enterKey)

	if recorder.savedProfile != "local" || recorder.savedKey != "" || recorder.voice == nil || !*recorder.voice || recorder.savedSecurityProfile != "default" {
		t.Fatalf("saved %q/%q, voice %v and profile %q, want local with no key, voice on and default", recorder.savedProfile, recorder.savedKey, recorder.voice, recorder.savedSecurityProfile)
	}
	if !m.isFinished || cmd == nil {
		t.Fatal("setup did not finish on the last enter")
	}
}

func TestSetupSavesTheChosenSecurityProfile(t *testing.T) {
	recorder := &setupRecorder{}
	m := recorder.buildSetupModel()
	m.step = setupSecurity

	m, _ = pressSetupKeys(m, tea.KeyPressMsg{Code: tea.KeyUp})
	if !strings.Contains(renderSetup(m), "paranoid can read   /work") {
		t.Fatalf("got view\n%s\nwant paranoid described once the cursor is on it", renderSetup(m))
	}
	m, _ = pressSetupKeys(m, enterKey)

	if recorder.savedSecurityProfile != "paranoid" || m.step != setupDone {
		t.Fatalf("saved %q at step %d, want paranoid and the done step next", recorder.savedSecurityProfile, m.step)
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

func TestSetupResumesAtTheRecordedStep(t *testing.T) {
	recorder := &setupRecorder{}
	m := recorder.buildSetupModelAt("mcp")
	if m.step != setupMCP {
		t.Fatalf("opened at step %d, want the recorded mcp step", m.step)
	}

	m, _ = pressSetupKeys(m, enterKey, tea.KeyPressMsg{Code: tea.KeyTab})

	if m.step != setupDone || strings.Join(recorder.recordedSteps, ",") != "security,done" {
		t.Fatalf("at step %d recorded %v, want security then done", m.step, recorder.recordedSteps)
	}
}

func TestSetupOpensAtTheFirstStepWhenTheRecordedStepIsUnknown(t *testing.T) {
	for _, startStep := range []string{"", "garbled\x00", "tools"} {
		if m := (&setupRecorder{}).buildSetupModelAt(startStep); m.step != setupProvider {
			t.Errorf("start step %q opened at step %d, want the provider step", startStep, m.step)
		}
	}
}

func TestSetupOffersADetectedLocalServerInPlaceOfItsProfile(t *testing.T) {
	recorder := &setupRecorder{localServers: []LocalServerSummary{
		{Profile: "local", Address: "127.0.0.1:11434", Model: "qwen3:14b", ContextLength: 40960, IsDetected: true},
		{Profile: "llamacpp", Address: "127.0.0.1:8080"},
	}}
	m := recorder.buildSetupModel()

	view := renderSetup(m)
	for _, want := range []string{"local      qwen3:14b                detected at 127.0.0.1:11434 · context 40960 tokens", "llamacpp", "not detected at 127.0.0.1:8080"} {
		if !strings.Contains(view, want) {
			t.Fatalf("got view\n%s\nwant %q", view, want)
		}
	}
	if strings.Contains(view, "tiny") {
		t.Fatalf("got view\n%s\nwant the detected server in place of the configured local profile", view)
	}

	m, _ = pressSetupKeys(m, tea.KeyPressMsg{Code: tea.KeyDown}, tea.KeyPressMsg{Code: tea.KeyDown}, enterKey)
	if m.step != setupProvider || recorder.savedProfile != "" || !strings.Contains(renderSetup(m), "✗ llamacpp: not detected") {
		t.Fatalf("got step %d, saved %q and view\n%s\nwant a server that is not running refused", m.step, recorder.savedProfile, renderSetup(m))
	}
	m, _ = pressSetupKeys(m, tea.KeyPressMsg{Code: tea.KeyUp}, enterKey)
	if m.step != setupVoice || recorder.savedProfile != "local" {
		t.Fatalf("got step %d and saved %q, want the detected server saved as local", m.step, recorder.savedProfile)
	}
}

func TestSetupTestsTheConnectionOnlyWhenAsked(t *testing.T) {
	recorder := &setupRecorder{}
	m := recorder.buildSetupModel()
	walked, _ := pressSetupKeys(m, tea.KeyPressMsg{Code: tea.KeyDown}, enterKey, enterKey, enterKey, enterKey, enterKey)
	if !walked.isFinished || len(recorder.testedProfiles) != 0 {
		t.Fatalf("finished %v after testing %v, want the whole wizard walked without a connection test", walked.isFinished, recorder.testedProfiles)
	}

	m, cmd := pressSetupKeys(m, typedKeys("t")...)
	if len(recorder.testedProfiles) != 0 || !strings.Contains(renderSetup(m), "testing…") {
		t.Fatalf("tested %v with view\n%s\nwant the test started off the UI thread", recorder.testedProfiles, renderSetup(m))
	}
	m, _ = pressSetupKeys(m, cmd())

	view := renderSetup(m)
	if strings.Join(recorder.testedProfiles, ",") != "cloud" || !strings.Contains(view, "test · cloud") || !strings.Contains(view, "   ✓ streaming") {
		t.Fatalf("tested %v with view\n%s\nwant cloud tested and one line per check", recorder.testedProfiles, view)
	}
}

func TestSetupCyclesAMultiModelServersModelWithTheArrowKeys(t *testing.T) {
	recorder := &setupRecorder{localServers: []LocalServerSummary{
		{Profile: "local", Address: "127.0.0.1:11434", Models: []string{"qwen3", "llama3", "gemma"}, Model: "qwen3", ContextLength: 1, IsDetected: true},
		{Profile: "llamacpp", Address: "127.0.0.1:8080", Models: []string{"tiny"}, Model: "tiny", IsDetected: true},
	}}
	m := recorder.buildSetupModel()
	m, _ = pressSetupKeys(m, tea.KeyPressMsg{Code: tea.KeyDown})
	if view := renderSetup(m); !strings.Contains(view, "‹qwen3› (1 of 3)") || strings.Contains(view, "‹tiny›") {
		t.Fatalf("got view\n%s\nwant a model position only on the server with a choice", view)
	}

	m, _ = pressSetupKeys(m, tea.KeyPressMsg{Code: tea.KeyRight})
	if view := renderSetup(m); !strings.Contains(view, "‹llama3› (2 of 3)") || !strings.Contains(view, "context 6000 tokens") {
		t.Fatalf("got view\n%s\nwant the second model with the context length looked up for it", view)
	}
	m, _ = pressSetupKeys(m, tea.KeyPressMsg{Code: tea.KeyLeft}, tea.KeyPressMsg{Code: tea.KeyLeft})
	if view := renderSetup(m); !strings.Contains(view, "‹gemma› (3 of 3)") {
		t.Fatalf("got view\n%s\nwant the choice to wrap to the last model", view)
	}
	m, _ = pressSetupKeys(m, tea.KeyPressMsg{Code: tea.KeyDown}, tea.KeyPressMsg{Code: tea.KeyRight})
	m, _ = pressSetupKeys(m, tea.KeyPressMsg{Code: tea.KeyUp}, enterKey)

	if got := strings.Join(recorder.chosenModels, ","); got != "local/llama3,local/qwen3,local/gemma" || recorder.savedProfile != "local" || m.step != setupVoice {
		t.Fatalf("chose %q and saved %q, want only the multi-model server asked, ending on gemma", got, recorder.savedProfile)
	}
}
