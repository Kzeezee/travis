package main

import (
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
