//go:build linux || darwin

package sandbox_test

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/Kzeezee/travis/internal/security/sandbox"
)

func TestResolveRunnerReadPathsNeverExposesHome(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skipf("no home folder: %v", err)
	}
	cases := map[string]struct{ executable, want string }{
		"homebrew runner gets its prefix": {"/opt/homebrew/bin/npx", "/opt/homebrew"},
		"system runner gets /usr":         {"/usr/bin/node", "/usr"},
		"nvm runner gets its version":     {filepath.Join(home, ".nvm/versions/node/v24/bin/npx"), filepath.Join(home, ".nvm/versions/node/v24")},
		"uv runner gets ~/.local":         {filepath.Join(home, ".local/bin/uvx"), filepath.Join(home, ".local")},
		"local binary gets its folder":    {"/opt/servers/mcp-notes", "/opt/servers"},
		"~/bin runner gets only ~/bin":    {filepath.Join(home, "bin/mcp-notes"), filepath.Join(home, "bin")},
		"binary in home gets only itself": {filepath.Join(home, "mcp-notes"), filepath.Join(home, "mcp-notes")},
		"/bin runner never gets /":        {"/bin/sh", "/bin"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			if got := sandbox.ResolveRunnerReadPaths(c.executable); !slices.Equal(got, []string{c.want}) {
				t.Errorf("ResolveRunnerReadPaths(%q) = %v, want [%s]", c.executable, got, c.want)
			}
		})
	}
}
