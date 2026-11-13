package main

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/Kzeezee/travis/internal/config"
	"github.com/Kzeezee/travis/internal/frontends/terminal"
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

func TestSetupProvidersSavesADetectedLocalServerAsItsProfile(t *testing.T) {
	paths := config.PathSet{ConfigDir: t.TempDir()}
	providers := &setupProviders{paths: paths, probes: []localServerProbe{
		{profile: "ollama", origin: "http://127.0.0.1:11434", detect: func(context.Context, string) openaicompat.LocalServer {
			return openaicompat.LocalServer{BaseURL: "http://127.0.0.1:11434/v1", Model: "qwen3:14b", ContextLength: 40960, IsDetected: true}
		}},
		{profile: "llamacpp", origin: "http://127.0.0.1:8080", detect: func(context.Context, string) openaicompat.LocalServer {
			return openaicompat.LocalServer{}
		}},
	}}

	summaries := providers.DetectLocalServers()
	want := []terminal.LocalServerSummary{
		{Profile: "ollama", Address: "127.0.0.1:11434", Model: "qwen3:14b", ContextLength: 40960, IsDetected: true},
		{Profile: "llamacpp", Address: "127.0.0.1:8080"},
	}
	if !slices.Equal(summaries, want) {
		t.Fatalf("got %+v, want %+v", summaries, want)
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
