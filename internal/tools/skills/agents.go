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

// loadWorkspaceAgentsBlock reads a candidate snapshot; the catalog gates its exposure on approval.
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
