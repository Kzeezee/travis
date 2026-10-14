package capabilities

import (
	"errors"
	"path/filepath"
	"slices"
	"testing"

	"github.com/Kzeezee/travis/internal/core"
)

func buildTestRoots(t *testing.T) Roots {
	base := t.TempDir()
	return Roots{
		Workspace: filepath.Join(base, "ws"),
		Temp:      filepath.Join(base, "tmp"),
		Home:      filepath.Join(base, "home"),
	}
}

func TestBuildProfileSetContents(t *testing.T) {
	r := buildTestRoots(t)
	r.ExecAllow = []string{"go"}
	r.HostAllow = []string{"API.GitHub.com"}
	ws, tmp, home := buildRootedGlob(r.Workspace, "**"), buildRootedGlob(r.Temp, "**"), buildRootedGlob(r.Home, "**")

	tests := []struct {
		profile string
		want    Set
	}{
		{"paranoid", Set{{Kind: core.CapabilityFSRead, Scope: ws}}},
		{"default", Set{
			{Kind: core.CapabilityFSRead, Scope: ws}, {Kind: core.CapabilityFSWrite, Scope: ws},
			{Kind: core.CapabilityExec, Scope: "go"}, {Kind: core.CapabilityNetworkHTTP, Scope: "api.github.com"},
		}},
		{"dev", Set{
			{Kind: core.CapabilityFSRead, Scope: home}, {Kind: core.CapabilityFSWrite, Scope: ws}, {Kind: core.CapabilityFSWrite, Scope: tmp},
			{Kind: core.CapabilityExec, Scope: "**"}, {Kind: core.CapabilityNetworkHTTP, Scope: "*"},
		}},
	}
	for _, tt := range tests {
		got, err := BuildProfileSet(tt.profile, r)
		if err != nil {
			t.Fatalf("BuildProfileSet(%q) error = %v", tt.profile, err)
		}
		if !slices.Equal(got, tt.want) {
			t.Errorf("BuildProfileSet(%q) = %v, want %v", tt.profile, got, tt.want)
		}
	}
}

func TestBuildProfileSetDefaultHasNoExecOrNetWithoutAllowlists(t *testing.T) {
	set, err := BuildProfileSet("default", buildTestRoots(t))
	if err != nil {
		t.Fatal(err)
	}
	for _, kind := range []core.CapabilityKind{core.CapabilityExec, core.CapabilityNetworkHTTP} {
		if slices.ContainsFunc(set, func(c core.Capability) bool { return c.Kind == kind }) {
			t.Errorf("default grants %s with an empty allowlist", kind)
		}
	}
}

func TestBuildProfileSetRejectsInvalidInput(t *testing.T) {
	r := buildTestRoots(t)
	relative := r
	relative.Workspace = "ws"
	badHost := r
	badHost.HostAllow = []string{"[unclosed"}

	tests := []struct {
		name    string
		profile string
		roots   Roots
	}{
		{"yolo", "yolo", r},
		{"unknown", "admin", r},
		{"empty", "", r},
		{"relative workspace", "paranoid", relative},
		{"bad host pattern", "default", badHost},
	}
	for _, tt := range tests {
		if _, err := BuildProfileSet(tt.profile, tt.roots); !errors.Is(err, core.ErrInvalid) {
			t.Errorf("%s: error = %v, want core.ErrInvalid", tt.name, err)
		}
	}
}

func TestSelectMatching(t *testing.T) {
	r := buildTestRoots(t)
	bracketRoot := filepath.Join(r.Home, "a[1]")
	set := Set{
		{Kind: core.CapabilityFSRead, Scope: buildRootedGlob(bracketRoot, "**")},
		{Kind: core.CapabilityNetworkHTTP, Scope: "*.github.com"},
		{Kind: core.CapabilitySecret, Scope: "GITHUB_TOKEN"},
	}

	tests := []struct {
		name              string
		kind              core.CapabilityKind
		target            string
		isCaseInsensitive bool
		isMatch           bool
	}{
		{"own root", core.CapabilityFSRead, filepath.Join(bracketRoot, "f.go"), false, true},
		{"sibling of bracket root", core.CapabilityFSRead, filepath.Join(r.Home, "a1", "f.go"), false, false},
		{"case differs, sensitive", core.CapabilityFSRead, filepath.Join(r.Home, "A[1]", "f.go"), false, false},
		{"case differs, insensitive", core.CapabilityFSRead, filepath.Join(r.Home, "A[1]", "f.go"), true, true},
		{"wrong kind", core.CapabilityFSWrite, filepath.Join(bracketRoot, "f.go"), false, false},
		{"host is always case-insensitive", core.CapabilityNetworkHTTP, "API.GitHub.com", false, true},
		{"host mismatch", core.CapabilityNetworkHTTP, "github.com.evil.test", false, false},
		{"secret exact", core.CapabilitySecret, "GITHUB_TOKEN", false, true},
		{"secret is not a glob", core.CapabilitySecret, "GITHUB_TOKEN2", false, false},
		{"audio never granted", core.CapabilityAudioCapture, "mic", false, false},
	}
	for _, tt := range tests {
		if _, got := set.SelectMatching(tt.kind, tt.target, tt.isCaseInsensitive); got != tt.isMatch {
			t.Errorf("%s: SelectMatching(%s, %q) = %v, want %v", tt.name, tt.kind, tt.target, got, tt.isMatch)
		}
	}
}
