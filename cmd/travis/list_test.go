package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/Kzeezee/travis/internal/config"
)

func writeUserConfigInSandbox(t *testing.T, content string) {
	t.Helper()
	useConfigSandbox(t)
	paths, err := config.BuildCurrentPathSet()
	if err != nil {
		t.Fatal(err)
	}
	writeConfigFile(t, paths.ReportUserConfigFile(), content)
}

func runListCommand(t *testing.T, args ...string) (string, error) {
	t.Helper()
	var out bytes.Buffer
	err := run(args, strings.NewReader(""), &out)
	return out.String(), err
}

func TestProviderListMarksTheActiveProfileAndLocalEndpoints(t *testing.T) {
	writeUserConfigInSandbox(t, `
[provider]
active = "local"
[provider.profiles.local]
kind     = "openai_compatible"
base_url = "http://127.0.0.1:8080/v1"
model    = "tiny"
`)

	out, err := runListCommand(t, "provider", "list")
	if err != nil {
		t.Fatal(err)
	}
	var localLine string
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, " local ") {
			localLine = line
		}
	}
	if !strings.HasPrefix(localLine, "* local") || !strings.Contains(localLine, "local, no key needed") || !strings.Contains(out, "  gemini") {
		t.Fatalf("got\n%s\nwant local marked active and keyless, and the built-in profiles listed", out)
	}
}

func TestMCPListShowsConfiguredServersWithSecretsScrubbed(t *testing.T) {
	writeUserConfigInSandbox(t, `
[mcp.servers.github]
command = "npx"
args    = ["-y", "github-mcp", "--token", "ghp_abcdefghijklmnopqrstuvwxyz0123"]
`)

	out, err := runListCommand(t, "mcp", "list")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "github") || !strings.Contains(out, "npx -y github-mcp --token") || strings.Contains(out, "ghp_") {
		t.Fatalf("got\n%s\nwant the server and its command with the token scrubbed", out)
	}
}

func TestMCPListSaysWhenNothingIsConfigured(t *testing.T) {
	useConfigSandbox(t)

	if out, err := runListCommand(t, "mcp", "list"); err != nil || !strings.Contains(out, "no MCP servers configured") {
		t.Fatalf("got %q, %v; want the empty-state hint", out, err)
	}
}

func TestListCommandsRejectUnknownArguments(t *testing.T) {
	useConfigSandbox(t)

	for _, args := range [][]string{{"provider"}, {"provider", "use"}, {"mcp", "add"}, {"models", "extra"}} {
		if _, err := runListCommand(t, args...); err == nil || !strings.Contains(err.Error(), "usage:") {
			t.Errorf("%v: got %v, want a usage error", args, err)
		}
	}
}
