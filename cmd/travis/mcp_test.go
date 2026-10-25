package main

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"github.com/Kzeezee/travis/internal/config"
	"github.com/Kzeezee/travis/internal/tools/mcp"
)

type fakeRegistry struct{ asked []mcp.PackageRef }

func (r *fakeRegistry) FetchProvenance(_ context.Context, ref mcp.PackageRef) (mcp.Provenance, error) {
	r.asked = append(r.asked, ref)
	return mcp.Provenance{Ref: ref, Publisher: "alice", WeeklyDownloads: 4120, DependencyCount: 3, Integrity: "sha512-abc",
		PublishedAt: time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)}, nil
}

func buildTestInstall(t *testing.T, answer string) (mcpInstall, *bytes.Buffer, *fakeRegistry) {
	t.Helper()
	useConfigSandbox(t)
	paths, err := config.BuildCurrentPathSet()
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	registry := &fakeRegistry{}
	return mcpInstall{in: strings.NewReader(answer), out: &out, isInteractive: true, lookup: registry, paths: paths,
		now: time.Date(2026, 10, 25, 0, 0, 0, 0, time.UTC)}, &out, registry
}

func loadConfiguredServers(t *testing.T) map[string]config.MCPServerCfg {
	t.Helper()
	cfg, _, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	return cfg.MCP.Servers
}

func TestMCPAddShowsTheProvenanceCardAndSavesOnYes(t *testing.T) {
	install, out, registry := buildTestInstall(t, "y\n")

	if err := install.addServer([]string{"github", "--allow-net", "--env", "GITHUB_TOKEN=abc", "--", "npx", "-y", "@scope/server@1.2.3"}); err != nil {
		t.Fatal(err)
	}

	for _, want := range []string{"@scope/server @ 1.2.3 (npm)", "publisher   alice", "(24 days ago)", "4120 last week", "3 direct", "sha512-abc", "GITHUB_TOKEN (stored in config.toml as plain text)", "third-party code"} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("got card\n%s\nwant %q", out, want)
		}
	}
	if len(registry.asked) != 1 {
		t.Fatalf("asked the registry %d times, want once", len(registry.asked))
	}
	got := loadConfiguredServers(t)["github"]
	if got.Command != "npx" || strings.Join(got.Args, " ") != "-y @scope/server@1.2.3" || !got.AllowNet || got.Env["GITHUB_TOKEN"] != "abc" {
		t.Fatalf("saved %+v, want the server as given", got)
	}
}

func TestMCPAddSavesNothingUnlessTheUserSaysYes(t *testing.T) {
	install, out, _ := buildTestInstall(t, "\n")

	if err := install.addServer([]string{"notes", "npx", "-y", "notes@1.0.0"}); err != nil {
		t.Fatal(err)
	}

	if _, ok := loadConfiguredServers(t)["notes"]; ok || !strings.Contains(out.String(), "not installed") {
		t.Fatalf("got\n%s\nwant nothing saved", out)
	}
	if !strings.Contains(out.String(), "without --allow-net that launch will fail") {
		t.Fatalf("got\n%s\nwant the first-launch network warning", out)
	}
}

func TestMCPAddRefusesPipedInputAndUnpinnedPackages(t *testing.T) {
	install, _, registry := buildTestInstall(t, "y\n")
	install.isInteractive = false
	if err := install.addServer([]string{"notes", "npx", "notes@1.0.0"}); err == nil || !strings.Contains(err.Error(), "confirmed at a terminal") {
		t.Fatalf("got %v, want a refusal for piped input", err)
	}

	install.isInteractive = true
	if err := install.addServer([]string{"notes", "npx", "-y", "notes@latest"}); err == nil || !strings.Contains(err.Error(), "exact version") {
		t.Fatalf("got %v, want a refusal for an unpinned package", err)
	}
	if len(registry.asked) != 0 || len(loadConfiguredServers(t)) != 0 {
		t.Fatal("a refused install still reached the registry or the config")
	}
}

func TestMCPAddRefusesANameItsToolsCouldNotUse(t *testing.T) {
	install, _, registry := buildTestInstall(t, "y\n")

	for _, name := range []string{"my.files", "has space", strings.Repeat("x", 33)} {
		if err := install.addServer([]string{name, "/opt/bin/my-server"}); err == nil || !strings.Contains(err.Error(), "letters, digits") {
			t.Errorf("%q: got %v, want a name refusal", name, err)
		}
	}
	if len(registry.asked) != 0 || len(loadConfiguredServers(t)) != 0 {
		t.Fatal("a refused name still reached the registry or the config")
	}
}

func TestMCPAddOfALocalCommandNeedsNoRegistry(t *testing.T) {
	install, out, registry := buildTestInstall(t, "yes\n")

	if err := install.addServer([]string{"mine", "/opt/bin/my-server", "--stdio"}); err != nil {
		t.Fatal(err)
	}

	if len(registry.asked) != 0 || !strings.Contains(out.String(), "local command") || loadConfiguredServers(t)["mine"].Command != "/opt/bin/my-server" {
		t.Fatalf("got\n%s\nwant a local install with no registry lookup", out)
	}
}

func TestMCPRemoveDeletesAConfiguredServer(t *testing.T) {
	install, _, _ := buildTestInstall(t, "y\n")
	if err := install.addServer([]string{"mine", "/opt/bin/my-server"}); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	if err := run([]string{"mcp", "remove", "mine"}, strings.NewReader(""), &out); err != nil || !strings.Contains(out.String(), `removed MCP server "mine"`) {
		t.Fatalf("got %q, %v; want the server removed", out.String(), err)
	}
	if len(loadConfiguredServers(t)) != 0 {
		t.Fatal("the server is still configured")
	}
}
