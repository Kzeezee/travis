package skills

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/Kzeezee/travis/internal/config"
	"github.com/Kzeezee/travis/internal/core"
)

const agentsFileName = "AGENTS.md"

// loadWorkspaceAgentsBlock reads <workspace>/AGENTS.md and wraps it as untrusted (D-23:
// "takes the same treatment as a workspace SKILL.md", §5.8/R-02). It is gated behind the
// same cfg.LoadWorkspace flag as workspace skills, not a separate one: both are
// workspace-authored prompt text with no approval UX yet (ticket 2D). A missing file or
// a read error is a diagnostic, never fatal — it just means no block is added.
func loadWorkspaceAgentsBlock(cfg config.SkillsCfg, paths config.PathSet, diagnostics io.Writer) string {
	if !cfg.LoadWorkspace {
		return ""
	}
	path := filepath.Join(paths.WorkspaceDir, agentsFileName)
	data, err := os.ReadFile(path)
	if err != nil {
		if !errors.Is(err, fs.ErrNotExist) {
			fmt.Fprintf(diagnostics, "skills: %s: %v\n", path, err)
		}
		return ""
	}
	return core.WrapUntrustedContent("workspace:"+agentsFileName, string(data))
}
