package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Kzeezee/travis/internal/config"
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
