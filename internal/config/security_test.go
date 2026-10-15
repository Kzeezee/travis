package config

import (
	"errors"
	"testing"

	"github.com/Kzeezee/travis/internal/core"
)

func TestLoadSecurityExecAllowFromUserConfig(t *testing.T) {
	paths := testPathSet()
	files := map[string]string{testUserConfigFile(paths): "[security]\nexec_allow = ['node', 'python3']\n"}
	cfg, diags, err := load(buildFileReaderFromMap(files), paths, nil, buildEnvLookupFromMap(nil))
	if err != nil || len(diags) != 0 || len(cfg.Security.ExecAllow) != 2 || cfg.Security.ExecAllow[0] != "node" {
		t.Fatalf("user exec allowlist = %+v, %v, %v", cfg, diags, err)
	}
}

func TestRedTeamGateWorkspaceCannotGrantExec(t *testing.T) {
	paths := testPathSet()
	files := map[string]string{paths.WorkspaceConfigFile: "[security]\nexec_allow = ['**']\n"}
	if _, _, err := load(buildFileReaderFromMap(files), paths, nil, buildEnvLookupFromMap(nil)); !errors.Is(err, core.ErrInvalid) {
		t.Fatalf("workspace exec grant = %v, want ErrInvalid", err)
	}
}

func TestLoadRejectsMalformedExecAllowPatterns(t *testing.T) {
	for _, pattern := range []string{"", " ", "[", " node "} {
		t.Run(pattern, func(t *testing.T) {
			cfg := buildDefaultConfig()
			cfg.Security.ExecAllow = []string{pattern}
			if !cfg.Validate().HasErrors() {
				t.Fatalf("invalid exec pattern %q was accepted", pattern)
			}
		})
	}
}
