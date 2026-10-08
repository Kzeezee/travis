package config

import (
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/bmatcuk/doublestar/v4"
)

func TestBuildDenyGlobsShape(t *testing.T) {
	home := t.TempDir()
	globs := buildDenyGlobsFromInputs(buildEnvLookupFromMap(nil), PathSet{StateDir: filepath.Join(home, "state")}, home)

	for _, glob := range globs.Write {
		if strings.HasPrefix(glob, "~") || strings.Contains(glob, `\`) {
			t.Errorf("glob %q starts with ~ or contains a backslash", glob)
		}
		if !strings.HasPrefix(glob, "**/") && !filepath.IsAbs(filepath.FromSlash(glob)) {
			t.Errorf("glob %q is neither absolute nor **/-rooted", glob)
		}
	}
	for _, glob := range globs.Read {
		if !slices.Contains(globs.Write, glob) {
			t.Errorf("read-deny %q missing from write-deny", glob)
		}
	}
	for _, glob := range []string{"**/.travis/**", "**/.git/hooks/**"} {
		if !slices.Contains(globs.Write, glob) || slices.Contains(globs.Read, glob) {
			t.Errorf("%q should be write-deny only", glob)
		}
	}
}

func TestBuildDenyGlobsMatches(t *testing.T) {
	root := filepath.ToSlash(t.TempDir())
	home := root + "/home"
	globs := buildDenyGlobsFromInputs(buildEnvLookupFromMap(nil), PathSet{StateDir: filepath.FromSlash(root + "/state")}, filepath.FromSlash(home))

	tests := []struct {
		target                      string
		isReadDenied, isWriteDenied bool
	}{
		{target: home + "/.ssh/id_rsa", isReadDenied: true, isWriteDenied: true},
		{target: root + "/ws/sub/.env.local", isReadDenied: true, isWriteDenied: true},
		{target: root + "/ws/.git/hooks/pre-commit", isWriteDenied: true},
		{target: root + "/ws/.travis/decisions.toml", isWriteDenied: true},
		{target: home + "/.bashrc", isWriteDenied: true},
		{target: root + "/state/sessions/abc.json", isReadDenied: true, isWriteDenied: true},
		{target: root + "/state/traces/run.jsonl", isReadDenied: true, isWriteDenied: true},
		{target: root + "/ws/main.go"},
	}
	for _, tt := range tests {
		if got := matchesAnyGlob(globs.Read, tt.target); got != tt.isReadDenied {
			t.Errorf("read-deny(%q) = %v, want %v", tt.target, got, tt.isReadDenied)
		}
		if got := matchesAnyGlob(globs.Write, tt.target); got != tt.isWriteDenied {
			t.Errorf("write-deny(%q) = %v, want %v", tt.target, got, tt.isWriteDenied)
		}
	}
}

func TestBuildRootedGlobEscapesRootMetacharacters(t *testing.T) {
	root := filepath.Join(t.TempDir(), "a[1]")
	glob := buildRootedGlob(root, ".ssh/**")
	slashRoot := filepath.ToSlash(root)

	if !doublestar.MatchUnvalidated(glob, slashRoot+"/.ssh/id_rsa") {
		t.Fatalf("%q should match its own root", glob)
	}
	if doublestar.MatchUnvalidated(glob, strings.Replace(slashRoot, "a[1]", "a1", 1)+"/.ssh/id_rsa") {
		t.Fatalf("%q matched a sibling root", glob)
	}
}

func TestBuildRootedGlobEscapesBackslashInRoot(t *testing.T) {
	if filepath.Separator == '\\' {
		t.Skip("backslash is a path separator here, not a name character")
	}
	parent := t.TempDir()
	glob := buildRootedGlob(filepath.Join(parent, `a\*b`), ".ssh/**")

	if !doublestar.MatchUnvalidated(glob, parent+`/a\*b/.ssh/id_rsa`) {
		t.Fatalf("%q should match its own root", glob)
	}
	if doublestar.MatchUnvalidated(glob, parent+`/a\xb/.ssh/id_rsa`) {
		t.Fatalf("%q matched a sibling root", glob)
	}
}

func matchesAnyGlob(globs []string, target string) bool {
	return slices.ContainsFunc(globs, func(glob string) bool { return doublestar.MatchUnvalidated(glob, target) })
}
