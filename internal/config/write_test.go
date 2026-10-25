package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func buildWritablePathSet(t *testing.T) PathSet {
	t.Helper()
	return PathSet{ConfigDir: filepath.Join(t.TempDir(), "config")}
}

func loadUserConfigOnly(t *testing.T, paths PathSet) *Config {
	t.Helper()
	cfg, _, err := load(os.ReadFile, paths, nil, func(string) (string, bool) { return "", false })
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

func TestSaveActiveProviderKeepsEverythingTheUserSet(t *testing.T) {
	paths := buildWritablePathSet(t)
	writeTestFile(t, paths.ReportUserConfigFile(), `
[brain]
max_steps = 7
future_setting = "kept"

[provider]
active = "cloud"
`)

	if err := SaveActiveProvider(paths, "gemini"); err != nil {
		t.Fatal(err)
	}

	cfg := loadUserConfigOnly(t, paths)
	if cfg.Provider.Active != "gemini" || cfg.Brain.MaxSteps != 7 {
		t.Fatalf("got active %q and max_steps %d, want gemini and the hand-set 7", cfg.Provider.Active, cfg.Brain.MaxSteps)
	}
	if raw, _ := os.ReadFile(paths.ReportUserConfigFile()); !strings.Contains(string(raw), "future_setting") {
		t.Fatalf("an unknown key was dropped:\n%s", raw)
	}
}

func TestUpdateUserConfigBacksUpACommentedFileOnlyOnce(t *testing.T) {
	paths := buildWritablePathSet(t)
	writeTestFile(t, paths.ReportUserConfigFile(), "# my notes\n[provider]\nactive = \"cloud\"\n")

	if err := SaveActiveProvider(paths, "gemini"); err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, paths.ReportUserConfigFile(), "# a later note\n[provider]\nactive = \"gemini\"\n")
	if err := SaveActiveProvider(paths, "deepseek"); err != nil {
		t.Fatal(err)
	}

	backup, err := os.ReadFile(paths.ReportUserConfigFile() + ".bak")
	if err != nil || !strings.Contains(string(backup), "# my notes") {
		t.Fatalf("got backup %q, %v; want the first commented version", backup, err)
	}
}

func TestUpdateUserConfigLeavesAnInvalidFileAlone(t *testing.T) {
	paths := buildWritablePathSet(t)
	writeTestFile(t, paths.ReportUserConfigFile(), "[provider\nbroken")

	if err := SaveActiveProvider(paths, "gemini"); err == nil {
		t.Fatal("got no error rewriting a file that is not valid TOML")
	}
	if raw, _ := os.ReadFile(paths.ReportUserConfigFile()); string(raw) != "[provider\nbroken" {
		t.Fatalf("the invalid file was changed to %q", raw)
	}
}

func TestSaveAndRemoveMCPServerRoundTrip(t *testing.T) {
	paths := buildWritablePathSet(t)
	server := MCPServerCfg{Command: "npx", Args: []string{"-y", "pkg@1.2.3"}, Env: map[string]string{"A": "b"}, AllowNet: true}

	if err := SaveMCPServer(paths, "files", server); err != nil {
		t.Fatal(err)
	}
	got := loadUserConfigOnly(t, paths).MCP.Servers["files"]
	if got.Command != "npx" || strings.Join(got.Args, " ") != "-y pkg@1.2.3" || got.Env["A"] != "b" || !got.AllowNet {
		t.Fatalf("got %+v, want the saved server", got)
	}

	if removed, err := RemoveMCPServer(paths, "files"); err != nil || !removed {
		t.Fatalf("got %v, %v; want the server removed", removed, err)
	}
	if removed, err := RemoveMCPServer(paths, "files"); err != nil || removed {
		t.Fatalf("got %v, %v; want a second removal to report nothing configured", removed, err)
	}
	if _, ok := loadUserConfigOnly(t, paths).MCP.Servers["files"]; ok {
		t.Fatal("the server is still configured")
	}
}

func writeTestFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}
