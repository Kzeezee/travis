package skills

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"path/filepath"

	"github.com/Kzeezee/travis/internal/config"
	"github.com/Kzeezee/travis/internal/core"
)

const agentsFileName = "AGENTS.md"

// loadWorkspaceAgentsBlock reads <workspace>/AGENTS.md and wraps it as untrusted (D-23:
// "takes the same treatment as a workspace SKILL.md", §5.8/R-02); the catalog holds it until
// approved. A missing file or a read error is a diagnostic, never fatal.
func loadWorkspaceAgentsBlock(paths config.PathSet, diagnostics io.Writer) string {
	path := filepath.Join(paths.WorkspaceDir, agentsFileName)
	data, err := readInstructionFile(path)
	if err != nil {
		if !errors.Is(err, fs.ErrNotExist) {
			fmt.Fprintf(diagnostics, "skills: %s: %v\n", path, err)
		}
		return ""
	}
	return core.WrapUntrustedContent("workspace:"+agentsFileName, string(data))
}
