package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// useConfigSandbox points TRAVIS_HOME and the workspace at fresh temp dirs and returns both.
func useConfigSandbox(t *testing.T) (travisHome, workspace string) {
	t.Helper()
	travisHome, workspace = t.TempDir(), t.TempDir()
	t.Setenv("TRAVIS_HOME", travisHome)
	t.Setenv("TRAVIS_PROVIDER_ACTIVE", "")
	t.Chdir(workspace)
	return travisHome, workspace
}

func runConfigCommand(t *testing.T, args ...string) (string, error) {
	t.Helper()
	var out bytes.Buffer
	err := run(append([]string{"config"}, args...), strings.NewReader(""), &out)
	return out.String(), err
}

func writeConfigFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestConfigPathMarksExistingFiles(t *testing.T) {
	travisHome, workspace := useConfigSandbox(t)
	writeConfigFile(t, filepath.Join(workspace, ".travis", "config.toml"), "")

	out, err := runConfigCommand(t, "path")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"user      " + filepath.Join(travisHome, "config.toml") + " (missing)",
		"workspace " + filepath.Join(workspace, ".travis", "config.toml") + " (exists)",
		"system    ",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output %q should contain %q", out, want)
		}
	}
}

func TestConfigCheckReportsInvalidConfigAsError(t *testing.T) {
	tests := []struct {
		name    string
		content string
		wantOut string
	}{
		{"valid", "[brain]\nmax_steps = 5\n", "config ok"},
		{"unknown provider.active", "[provider]\nactive = \"nope\"\n", `error: provider.active "nope"`},
		{"typo under security", "[security]\nexec_alow = [\"git\"]\n", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			travisHome, _ := useConfigSandbox(t)
			writeConfigFile(t, filepath.Join(travisHome, "config.toml"), tt.content)

			out, err := runConfigCommand(t, "check")

			if wantErr := tt.name != "valid"; (err != nil) != wantErr {
				t.Fatalf("check error = %v, wantErr %v", err, wantErr)
			}
			if !strings.Contains(out, tt.wantOut) {
				t.Errorf("output %q should contain %q", out, tt.wantOut)
			}
			if err != nil && tt.wantOut == "" && !strings.Contains(err.Error(), "exec_alow") {
				t.Errorf("error %q should name the misspelled key", err)
			}
		})
	}
}

func TestConfigCheckPrintsWorkspaceWarningsWithoutFailing(t *testing.T) {
	_, workspace := useConfigSandbox(t)
	writeConfigFile(t, filepath.Join(workspace, ".travis", "config.toml"), "[provider]\nactive = \"evil\"\n")

	out, err := runConfigCommand(t, "check")

	if err != nil {
		t.Fatalf("a workspace [provider] table is only a warning, got %v", err)
	}
	if !strings.Contains(out, "warning: ") || !strings.Contains(out, "[provider] may not be set by the workspace layer") {
		t.Errorf("output %q should print the workspace warning", out)
	}
}

func TestConfigShowPrintsMergedTOMLWithoutCredentials(t *testing.T) {
	travisHome, _ := useConfigSandbox(t)
	t.Setenv("OPENAI_API_KEY", "canary-openai-key")
	t.Setenv("DEEPSEEK_API_KEY", "canary-deepseek-key")
	writeConfigFile(t, filepath.Join(travisHome, "config.toml"), `
[mcp.servers.notes]
command = "notes-server"
[mcp.servers.notes.env]
NOTES_TOKEN = "canary-mcp-token"
`)

	out, err := runConfigCommand(t, "show")
	if err != nil {
		t.Fatal(err)
	}
	for _, canary := range []string{"canary-openai-key", "canary-deepseek-key", "canary-mcp-token"} {
		if strings.Contains(out, canary) {
			t.Errorf("show printed secret %q:\n%s", canary, out)
		}
	}
	for _, want := range []string{
		`active = 'cloud'`,
		"[provider.profiles.deepseek]",
		`ref = 'env://DEEPSEEK_API_KEY'`,
		`NOTES_TOKEN = '<redacted>'`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("show output should contain %q:\n%s", want, out)
		}
	}
}

func TestConfigInitWritesStarterThenRefusesToOverwrite(t *testing.T) {
	travisHome, workspace := useConfigSandbox(t)
	userFile := filepath.Join(travisHome, "config.toml")

	if _, err := runConfigCommand(t, "init"); err != nil {
		t.Fatal(err)
	}
	if got, err := os.ReadFile(userFile); err != nil || string(got) != starterConfig {
		t.Fatalf("user config = %q, %v; want the starter file", got, err)
	}
	if _, err := os.Stat(filepath.Join(workspace, ".travis")); !os.IsNotExist(err) {
		t.Errorf("init must write only the user config path; workspace .travis stat err = %v", err)
	}
	if _, err := runConfigCommand(t, "check"); err != nil {
		t.Errorf("the starter file must pass check: %v", err)
	}

	writeConfigFile(t, userFile, "# mine\n")
	if _, err := runConfigCommand(t, "init"); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("second init error = %v, want an already-exists refusal", err)
	}
	if got, _ := os.ReadFile(userFile); string(got) != "# mine\n" {
		t.Errorf("init overwrote the existing file: %q", got)
	}
}

func TestConfigRejectsMissingOrUnknownSubcommand(t *testing.T) {
	useConfigSandbox(t)
	for _, args := range [][]string{nil, {"bogus"}, {"path", "extra"}} {
		if _, err := runConfigCommand(t, args...); err == nil {
			t.Errorf("config %q should fail", args)
		}
	}
}
