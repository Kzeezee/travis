package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/Kzeezee/travis/internal/config"
	"github.com/Kzeezee/travis/internal/core"
	"github.com/Kzeezee/travis/internal/providers/openaicompat"
)

// Each profile is described by what capabilities.BuildProfileSet grants it, not by the config's allowlists alone.
func TestDescribeSecurityFollowsTheChosenProfile(t *testing.T) {
	cfg := &config.Config{Security: config.SecurityCfg{ExecAllow: []string{"npx"}, HostAllow: []string{"example.com"}}}
	paths := config.PathSet{WorkspaceDir: "/work"}
	for profile, want := range map[string][]string{
		"paranoid": {"can write            nothing", "can run              nothing", "can reach            nothing"},
		"default":  {"can write            the same folder", "can run              npx,", "can reach            example.com"},
		"dev":      {"can read             your home folder", "can run              any command", "can reach            any host"},
	} {
		description := strings.Join(describeSecurity(cfg, paths, profile), "\n")
		for _, line := range append(want, "OS sandbox") {
			if !strings.Contains(description, line) {
				t.Errorf("%s description\n%s\nis missing %q", profile, description, line)
			}
		}
	}
}

func TestSetupProgressResumesUntilSetupFinishes(t *testing.T) {
	paths := config.PathSet{ConfigDir: t.TempDir(), StateDir: filepath.Join(t.TempDir(), "state")}
	if step := readSetupProgress(paths.StateDir); step != "" {
		t.Fatalf("read %q with no progress saved, want empty", step)
	}

	recordSetupProgress(paths.StateDir, "security")
	if step := readSetupProgress(paths.StateDir); step != "security" {
		t.Fatalf("read %q, want the recorded security step", step)
	}
	if err := finishSetup(paths, "cloud"); err != nil {
		t.Fatal(err)
	}

	if step := readSetupProgress(paths.StateDir); step != "" {
		t.Fatalf("read %q after finishing, want the progress cleared", step)
	}
	if _, err := os.Stat(paths.ReportUserConfigFile()); err != nil {
		t.Fatalf("finishing left no user config: %v", err)
	}
}

func buildTwoModelOllamaProbe(contextLengths map[string]int, asked *[]string) localServerProbe {
	return localServerProbe{profile: "ollama", origin: "http://127.0.0.1:11434",
		detect: func(context.Context, string) openaicompat.LocalServer {
			return openaicompat.LocalServer{BaseURL: "http://127.0.0.1:11434/v1", Models: []string{"qwen3:14b", "llama3:8b"}, IsDetected: true}
		},
		lookUpContextLength: func(_ context.Context, _, model string) int {
			*asked = append(*asked, model)
			return contextLengths[model]
		}}
}

func TestSetupProvidersSavesADetectedLocalServerAsItsProfile(t *testing.T) {
	paths := config.PathSet{ConfigDir: t.TempDir()}
	var asked []string
	providers := &setupProviders{paths: paths, probes: []localServerProbe{
		buildTwoModelOllamaProbe(map[string]int{"qwen3:14b": 40960}, &asked),
		{profile: "llamacpp", origin: "http://127.0.0.1:8080", detect: func(context.Context, string) openaicompat.LocalServer {
			return openaicompat.LocalServer{}
		}},
	}}

	summaries := providers.DetectLocalServers()
	if len(summaries) != 2 || summaries[1].Profile != "llamacpp" || summaries[1].IsDetected {
		t.Fatalf("got %+v, want llamacpp not detected", summaries)
	}
	ollama := summaries[0]
	if ollama.Address != "127.0.0.1:11434" || ollama.Model != "qwen3:14b" || ollama.ContextLength != 40960 || !ollama.IsDetected ||
		!slices.Equal(ollama.Models, []string{"qwen3:14b", "llama3:8b"}) || !slices.Equal(asked, []string{"qwen3:14b"}) {
		t.Fatalf("got %+v after asking about %v, want every model listed, the first chosen and only its context length looked up", ollama, asked)
	}
	if err := providers.SaveProvider("ollama", ""); err != nil {
		t.Fatal(err)
	}

	saved, err := os.ReadFile(paths.ReportUserConfigFile())
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range []string{"active = 'ollama'", "base_url = 'http://127.0.0.1:11434/v1'", "model = 'qwen3:14b'", "context_length = 40960"} {
		if !strings.Contains(string(saved), line) {
			t.Errorf("saved config\n%s\nis missing %q", saved, line)
		}
	}
}

func TestSetupProvidersSavesAndTestsTheChosenLocalModel(t *testing.T) {
	var testedModel string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct{ Model string }
		_ = json.NewDecoder(r.Body).Decode(&request)
		testedModel = request.Model
		http.Error(w, "stop here", http.StatusInternalServerError)
	}))
	t.Cleanup(server.Close)
	paths := config.PathSet{ConfigDir: t.TempDir()}
	var asked []string
	probe := buildTwoModelOllamaProbe(map[string]int{"qwen3:14b": 40960, "llama3:8b": 8192}, &asked)
	probe.detect = func(context.Context, string) openaicompat.LocalServer {
		return openaicompat.LocalServer{BaseURL: server.URL + "/v1", Models: []string{"qwen3:14b", "llama3:8b"}, IsDetected: true}
	}
	providers := &setupProviders{paths: paths, probes: []localServerProbe{probe}}
	providers.DetectLocalServers()

	if got := providers.ChooseLocalModel("ollama", "llama3:8b"); got != 8192 {
		t.Fatalf("got context length %d, want the chosen model's 8192", got)
	}
	providers.TestConnection("ollama")
	if err := providers.SaveProvider("ollama", ""); err != nil {
		t.Fatal(err)
	}

	saved, err := os.ReadFile(paths.ReportUserConfigFile())
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range []string{"model = 'llama3:8b'", "context_length = 8192"} {
		if !strings.Contains(string(saved), line) {
			t.Errorf("saved config\n%s\nis missing %q", saved, line)
		}
	}
	if testedModel != "llama3:8b" || !slices.Equal(asked, []string{"qwen3:14b", "llama3:8b"}) {
		t.Fatalf("tested %q after asking about %v, want the chosen model tested and looked up once", testedModel, asked)
	}
}

func TestRunConnectionTestReportsEachCheck(t *testing.T) {
	answer := func(chunks ...core.Chunk) scriptedStep {
		return func(*testing.T, core.ProviderRequest) []core.Chunk { return chunks }
	}
	for name, test := range map[string]struct {
		step scriptedStep
		want []string
	}{
		"tool called": {answer(buildToolCallChunks("1", "ping", "{}")...), []string{"✓ reachable (0 ms)", "✓ streaming", "✓ tool call"}},
		"text only":   {answer(core.Chunk{Text: "pong"}), []string{"✓ reachable (0 ms)", "✓ streaming", "✗ tool call: the model answered without calling the tool"}},
		"empty reply": {answer(), []string{"✓ reachable (0 ms)", "✗ streaming: the reply was empty", "✗ tool call: the reply was empty"}},
		"unreachable": {nil, []string{"✗ reachable: scripted provider: no step 1"}},
	} {
		provider := &scriptedProvider{t: t}
		if test.step != nil {
			provider.steps = []scriptedStep{test.step}
		}

		got := runConnectionTest(context.Background(), provider, "qwen3:14b", core.FixedClock{})

		if !slices.Equal(got, test.want) {
			t.Errorf("%s: got %q, want %q", name, got, test.want)
		}
		if len(provider.requests) == 1 && (len(provider.requests[0].Tools) != 1 || provider.requests[0].Model != "qwen3:14b") {
			t.Errorf("%s: sent %+v, want one tiny request offering one tool", name, provider.requests[0])
		}
	}
}
