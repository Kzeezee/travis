//go:build linux || darwin

package sandbox_test

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/Kzeezee/travis/internal/security/sandbox"
)

// A Homebrew-style prefix: the runner gets every folder but var, where local databases live.
func TestResolveRunnerReadPathsGrantsThePrefixWithoutItsDataFolder(t *testing.T) {
	prefix := t.TempDir()
	for _, child := range []string{"bin", "Cellar", "etc", "lib", "opt", "var"} {
		if err := os.Mkdir(filepath.Join(prefix, child), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	got := sandbox.ResolveRunnerReadPaths(filepath.Join(prefix, "bin", "npx"))
	var want []string
	for _, child := range []string{"Cellar", "bin", "etc", "lib", "opt"} {
		want = append(want, filepath.Join(prefix, child))
	}
	if !slices.Equal(got, want) {
		t.Fatalf("ResolveRunnerReadPaths = %v, want %v", got, want)
	}
}

func TestResolveRunnerReadPathsNeverExposesHome(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skipf("no home folder: %v", err)
	}
	cases := map[string]struct{ executable, want string }{
		"local binary gets its folder":    {"/opt/servers/mcp-notes", "/opt/servers"},
		"~/bin runner gets only ~/bin":    {filepath.Join(home, "bin/mcp-notes"), filepath.Join(home, "bin")},
		"binary in home gets only itself": {filepath.Join(home, "mcp-notes"), filepath.Join(home, "mcp-notes")},
		"/bin runner never gets /":        {"/bin/sh", "/bin"},
		"missing prefix falls back":       {"/nonexistent-prefix/bin/npx", "/nonexistent-prefix/bin"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			if got := sandbox.ResolveRunnerReadPaths(c.executable); !slices.Equal(got, []string{c.want}) {
				t.Errorf("ResolveRunnerReadPaths(%q) = %v, want [%s]", c.executable, got, c.want)
			}
		})
	}
}
