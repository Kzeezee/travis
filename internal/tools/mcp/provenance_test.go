package mcp

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Kzeezee/travis/internal/core"
)

func TestResolvePackageRefRequiresAnExactVersion(t *testing.T) {
	cases := []struct {
		command string
		args    []string
		want    PackageRef
		isErr   bool
	}{
		{command: "npx", args: []string{"-y", "@modelcontextprotocol/server-github@0.6.2"}, want: PackageRef{RegistryNPM, "@modelcontextprotocol/server-github", "0.6.2"}},
		{command: "npx.cmd", args: []string{"--package=mcp-notes@1.0.0-beta.1", "notes"}, want: PackageRef{RegistryNPM, "mcp-notes", "1.0.0-beta.1"}},
		{command: "/usr/local/bin/uvx", args: []string{"mcp-server-time==2024.1"}, want: PackageRef{RegistryPyPI, "mcp-server-time", "2024.1"}},
		{command: "uvx", args: []string{"--from", "mcp-git@1.2.0", "mcp-server-git"}, want: PackageRef{RegistryPyPI, "mcp-git", "1.2.0"}},
		{command: "npx", args: []string{"-y", "@scope/server"}, isErr: true},
		{command: "npx", args: []string{"server@latest"}, isErr: true},
		{command: "npx", args: []string{"server@^1.2.0"}, isErr: true},
		{command: "uvx", args: []string{"mcp-server-time>=1.0"}, isErr: true},
		{command: "npx", args: []string{"-y"}, isErr: true},
	}
	for _, tc := range cases {
		got, isRegistry, err := ResolvePackageRef(tc.command, tc.args)
		if !isRegistry || (err != nil) != tc.isErr || (!tc.isErr && got != tc.want) {
			t.Errorf("%s %v: got %+v, %v, %v; want %+v, error %v", tc.command, tc.args, got, isRegistry, err, tc.want, tc.isErr)
		}
	}
	if _, isRegistry, err := ResolvePackageRef("/opt/bin/my-server", []string{"--stdio"}); isRegistry || err != nil {
		t.Fatalf("a local binary got %v, %v; want no registry and no error", isRegistry, err)
	}
}

func TestParsePackageShorthandRoundTripsThroughTheLaunchCommand(t *testing.T) {
	cases := []struct {
		spec  string
		want  PackageRef
		isErr bool
	}{
		{spec: "npm:@scope/server", want: PackageRef{RegistryNPM, "@scope/server", ""}},
		{spec: "npm:@scope/server@1.2.3", want: PackageRef{RegistryNPM, "@scope/server", "1.2.3"}},
		{spec: "pypi:mcp-server-time", want: PackageRef{RegistryPyPI, "mcp-server-time", ""}},
		{spec: "pypi:mcp-server-time==2024.1", want: PackageRef{RegistryPyPI, "mcp-server-time", "2024.1"}},
		{spec: "npm:server@latest", isErr: true},
		{spec: "pypi:mcp-server-time>=1.0", isErr: true},
		{spec: "npm:", isErr: true},
		{spec: "pypi:-rf", isErr: true},
	}
	for _, tc := range cases {
		got, isShorthand, err := ParsePackageShorthand(tc.spec)
		if !isShorthand || (err != nil) != tc.isErr || (!tc.isErr && got != tc.want) {
			t.Errorf("%s: got %+v, %v, %v; want %+v, error %v", tc.spec, got, isShorthand, err, tc.want, tc.isErr)
		}
	}
	if _, isShorthand, err := ParsePackageShorthand("npx"); isShorthand || err != nil {
		t.Fatalf("a plain command got %v, %v; want no shorthand and no error", isShorthand, err)
	}

	for _, ref := range []PackageRef{{RegistryNPM, "@scope/server", "1.2.3"}, {RegistryPyPI, "mcp-server-time", "2024.1"}} {
		command, args := ref.BuildLaunch()
		if got, _, err := ResolvePackageRef(command, args); err != nil || got != ref {
			t.Errorf("%+v launched as %s %v resolves back to %+v, %v", ref, command, args, got, err)
		}
	}
}

func buildTestRegistryLookup(t *testing.T, routes map[string]string) RegistryLookup {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, ok := routes[r.URL.EscapedPath()]
		if !ok {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(server.Close)
	return RegistryLookup{client: server.Client(), npmBaseURL: server.URL + "/npm", npmDownloadsURL: server.URL + "/downloads", pypiBaseURL: server.URL + "/pypi"}
}

func TestFetchProvenanceReadsTheNPMVersionDocument(t *testing.T) {
	lookup := buildTestRegistryLookup(t, map[string]string{
		"/npm/@scope%2Fserver": `{"time":{"1.2.3":"2026-09-01T10:00:00.000Z"},"versions":{"1.2.3":{
			"dependencies":{"a":"1","b":"2"},"_npmUser":{"name":"alice"},"dist":{"integrity":"sha512-abc"}}}}`,
		"/downloads/@scope/server": `{"downloads":4120}`,
	})

	got, err := lookup.FetchProvenance(context.Background(), PackageRef{RegistryNPM, "@scope/server", "1.2.3"})
	if err != nil {
		t.Fatal(err)
	}
	if got.Publisher != "alice" || got.DependencyCount != 2 || got.Integrity != "sha512-abc" || got.WeeklyDownloads != 4120 || got.PublishedAt.Year() != 2026 {
		t.Fatalf("got %+v, want every field from the registry", got)
	}
	if _, err := lookup.FetchProvenance(context.Background(), PackageRef{RegistryNPM, "@scope/server", "9.9.9"}); !errors.Is(err, core.ErrInvalid) {
		t.Fatalf("got %v for a version npm does not have, want ErrInvalid", err)
	}
}

func TestFetchProvenancePinsTheLatestVersionWhenNoneIsGiven(t *testing.T) {
	lookup := buildTestRegistryLookup(t, map[string]string{
		"/npm/server":         `{"dist-tags":{"latest":"2.0.0"},"versions":{"1.0.0":{},"2.0.0":{"_npmUser":{"name":"alice"}}}}`,
		"/pypi/mcp-time/json": `{"info":{"version":"2026.1","author":"bob"}}`,
	})

	npm, err := lookup.FetchProvenance(context.Background(), PackageRef{RegistryNPM, "server", ""})
	if err != nil || npm.Ref.Version != "2.0.0" || npm.Publisher != "alice" {
		t.Fatalf("got %+v, %v; want npm's latest, 2.0.0", npm, err)
	}
	pypi, err := lookup.FetchProvenance(context.Background(), PackageRef{RegistryPyPI, "mcp-time", ""})
	if err != nil || pypi.Ref.Version != "2026.1" || pypi.Publisher != "bob" {
		t.Fatalf("got %+v, %v; want PyPI's latest, 2026.1", pypi, err)
	}
}

func TestFetchProvenanceReadsThePyPIReleaseAndSkipsExtras(t *testing.T) {
	lookup := buildTestRegistryLookup(t, map[string]string{
		"/pypi/mcp-time/2024.1/json": `{"info":{"author":"","maintainer":"bob","requires_dist":["httpx","pydantic","pytest; extra == \"test\""]},
			"urls":[{"upload_time_iso_8601":"2024-01-02T03:04:05.000000Z","digests":{"sha256":"ff00"}}]}`,
	})

	got, err := lookup.FetchProvenance(context.Background(), PackageRef{RegistryPyPI, "mcp-time", "2024.1"})
	if err != nil {
		t.Fatal(err)
	}
	if got.Publisher != "bob" || got.DependencyCount != 2 || got.Integrity != "sha256-ff00" || got.WeeklyDownloads != -1 || got.PublishedAt.Year() != 2024 {
		t.Fatalf("got %+v, want the maintainer, two required deps, the digest and unknown downloads", got)
	}
}
