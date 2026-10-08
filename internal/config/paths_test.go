package config

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Kzeezee/travis/internal/core"
)

func TestBuildPathSetFromInputsUsesTravisHome(t *testing.T) {
	root := t.TempDir()
	workspace := filepath.Join(t.TempDir(), "workspace")

	got, err := buildPathSetFromInputs(buildEnvLookupFromMap(map[string]string{"TRAVIS_HOME": root}), filepath.Join(t.TempDir(), "home"), workspace)
	if err != nil {
		t.Fatal(err)
	}

	if got.ConfigDir != root {
		t.Fatalf("ConfigDir = %q, want %q", got.ConfigDir, root)
	}
	if want := filepath.Join(root, "state"); got.StateDir != want {
		t.Fatalf("StateDir = %q, want %q", got.StateDir, want)
	}
	if want := filepath.Join(root, "cache"); got.CacheDir != want {
		t.Fatalf("CacheDir = %q, want %q", got.CacheDir, want)
	}
	if want := filepath.Join(root, "config.toml"); got.SystemConfigFile != want {
		t.Fatalf("SystemConfigFile = %q, want %q", got.SystemConfigFile, want)
	}
	if want := filepath.Join(workspace, ".travis", "config.toml"); got.WorkspaceConfigFile != want {
		t.Fatalf("WorkspaceConfigFile = %q, want %q", got.WorkspaceConfigFile, want)
	}
}

func TestBuildPathSetFromInputsKeepsTheRuntimeDirOutOfTravisHome(t *testing.T) {
	root := t.TempDir()
	workspace := filepath.Join(t.TempDir(), "workspace")

	got, err := buildPathSetFromInputs(buildEnvLookupFromMap(map[string]string{"TRAVIS_HOME": root}), filepath.Join(t.TempDir(), "home"), workspace)
	if err != nil {
		t.Fatal(err)
	}
	if !filepath.IsAbs(got.RuntimeDir) || strings.HasPrefix(got.RuntimeDir, root) {
		t.Fatalf("RuntimeDir = %q, want an absolute OS directory outside TRAVIS_HOME %q", got.RuntimeDir, root)
	}
}

func TestBuildPathSetFromInputsRejectsRelativeTravisHome(t *testing.T) {
	workspace := filepath.Join(t.TempDir(), "workspace")

	_, err := buildPathSetFromInputs(buildEnvLookupFromMap(map[string]string{"TRAVIS_HOME": "relative"}), filepath.Join(t.TempDir(), "home"), workspace)
	if !errors.Is(err, core.ErrInvalid) {
		t.Fatalf("buildPathSetFromInputs() error = %v, want %v", err, core.ErrInvalid)
	}
}

func TestPathSetResolve(t *testing.T) {
	root := t.TempDir()
	paths := PathSet{
		ConfigDir:    filepath.Join(root, "config"),
		StateDir:     filepath.Join(root, "state"),
		CacheDir:     filepath.Join(root, "cache"),
		WorkspaceDir: filepath.Join(root, "workspace"),
	}

	tests := []struct {
		name  string
		value string
		want  string
	}{
		{name: "state prefix", value: "state:traces/run.jsonl", want: filepath.Join(paths.StateDir, "traces", "run.jsonl")},
		{name: "windows separators", value: "config:profiles\\dev.toml", want: filepath.Join(paths.ConfigDir, "profiles", "dev.toml")},
		{name: "relative path", value: "notes/today.md", want: filepath.Join(paths.WorkspaceDir, "notes", "today.md")},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := paths.Resolve(tt.value)
			if err != nil {
				t.Fatal(err)
			}
			if got != tt.want {
				t.Fatalf("Resolve(%q) = %q, want %q", tt.value, got, tt.want)
			}
		})
	}
}

func TestPathSetResolveRejectsUnknownPrefix(t *testing.T) {
	paths := PathSet{WorkspaceDir: t.TempDir()}

	_, err := paths.Resolve("secret:openai")
	if !errors.Is(err, core.ErrInvalid) {
		t.Fatalf("Resolve() error = %v, want %v", err, core.ErrInvalid)
	}
}

func buildEnvLookupFromMap(values map[string]string) envLookup {
	return func(envKey string) (string, bool) {
		value, ok := values[envKey]
		return value, ok
	}
}
