package config

import (
	"errors"
	"fmt"
	"path/filepath"
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

func TestLoadRequiresTheSandboxByDefault(t *testing.T) {
	cfg, _, err := load(buildFileReaderFromMap(nil), testPathSet(), nil, buildEnvLookupFromMap(nil))
	if err != nil || !cfg.Security.RequireSandbox {
		t.Fatalf("default require_sandbox = %v, %v; want true", cfg.Security.RequireSandbox, err)
	}
}

func TestLoadUserConfigCanTurnOffRequireSandbox(t *testing.T) {
	paths := testPathSet()
	files := map[string]string{testUserConfigFile(paths): "[security]\nrequire_sandbox = false\n"}
	cfg, diags, err := load(buildFileReaderFromMap(files), paths, nil, buildEnvLookupFromMap(nil))
	if err != nil || len(diags) != 0 || cfg.Security.RequireSandbox {
		t.Fatalf("user require_sandbox = false gave %v, %v, %v", cfg.Security.RequireSandbox, diags, err)
	}
}

// A cloned repo must never lower the sandbox requirement.
func TestRedTeamGateWorkspaceCannotTurnOffRequireSandbox(t *testing.T) {
	paths := testPathSet()
	files := map[string]string{paths.WorkspaceConfigFile: "[security]\nrequire_sandbox = false\n"}
	if _, _, err := load(buildFileReaderFromMap(files), paths, nil, buildEnvLookupFromMap(nil)); !errors.Is(err, core.ErrInvalid) {
		t.Fatalf("workspace require_sandbox = false gave %v, want ErrInvalid", err)
	}
}

func TestLoadMCPServerSandboxGrants(t *testing.T) {
	paths := testPathSet()
	notes := t.TempDir()
	out := filepath.Join(notes, "out")
	files := map[string]string{testUserConfigFile(paths): fmt.Sprintf("[mcp.servers.notes]\ncommand = 'npx'\nallow_net = true\nread_paths = ['%s']\nwrite_paths = ['%s']\n", notes, out)}
	cfg, diags, err := load(buildFileReaderFromMap(files), paths, nil, buildEnvLookupFromMap(nil))
	server := cfg.MCP.Servers["notes"]
	if err != nil || len(diags) != 0 || !server.AllowNet || server.ReadPaths[0] != notes || server.WritePaths[0] != out {
		t.Fatalf("mcp server grants = %+v, %v, %v", server, diags, err)
	}
}

func TestValidateRejectsUnsafeMCPServerSettings(t *testing.T) {
	cases := map[string]func(*Config){
		"relative read path":       func(c *Config) { c.MCP.Servers["notes"] = MCPServerCfg{Command: "npx", ReadPaths: []string{"notes"}} },
		"relative write path":      func(c *Config) { c.MCP.Servers["notes"] = MCPServerCfg{Command: "npx", WritePaths: []string{"../out"}} },
		"name that leaves scratch": func(c *Config) { c.MCP.Servers["../notes"] = MCPServerCfg{Command: "npx"} },
	}
	for name, breakConfig := range cases {
		t.Run(name, func(t *testing.T) {
			cfg := buildDefaultConfig()
			cfg.MCP.Servers = map[string]MCPServerCfg{}
			breakConfig(&cfg)
			if !cfg.Validate().HasErrors() {
				t.Fatalf("%s was accepted", name)
			}
		})
	}
}
