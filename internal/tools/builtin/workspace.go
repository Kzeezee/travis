package builtin

import (
	"errors"
	"io/fs"
	"path/filepath"
	"strings"

	"github.com/Kzeezee/travis/internal/config"
	"github.com/Kzeezee/travis/internal/core"
)

// workspaceRoot confines model-supplied paths to the workspace; it moves inside the Broker in Phase 2.
type workspaceRoot struct {
	paths config.PathSet
	root  string
	// resolvedRoot is root with symlinks evaluated, so confinement compares like with like.
	resolvedRoot string
}

func buildWorkspaceRoot(dir string) workspaceRoot {
	root := filepath.Clean(dir)
	resolvedRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		resolvedRoot = root
	}
	return workspaceRoot{paths: config.PathSet{WorkspaceDir: root}, root: root, resolvedRoot: resolvedRoot}
}

// resolvePath turns the path the model asked for into a real path on disk, allowed only if
// it stays inside the workspace once symlinks are followed. expectedArgs is echoed back when
// the path itself is malformed, so the model sees the calling tool's argument shape.
func (w workspaceRoot) resolvePath(requested string, expectedArgs map[string]string) (string, core.ToolOutput, bool) {
	outside := buildErrorOutput(errorPathOutsideWorkspace, map[string]any{
		"path":      requested,
		"workspace": w.root,
	})

	resolved, err := w.paths.Resolve(requested)
	if err != nil {
		return "", buildErrorOutput(errorInvalidArguments, map[string]any{
			"detail":   err.Error(),
			"expected": expectedArgs,
		}), false
	}
	if !filepath.IsAbs(resolved) || !isInsideRoot(w.root, resolved) {
		return "", outside, false
	}

	evaluated, err := filepath.EvalSymlinks(resolved)
	if errors.Is(err, fs.ErrNotExist) {
		return "", buildErrorOutput(errorFileNotFound, map[string]any{"path": requested}), false
	}
	if err != nil {
		return "", buildErrorOutput(errorReadFailed, map[string]any{"path": requested, "detail": err.Error()}), false
	}
	if !w.containsResolvedPath(evaluated) {
		return "", outside, false
	}
	return evaluated, core.ToolOutput{}, true
}

// containsResolvedPath reports whether a symlink-evaluated path lies inside the workspace.
func (w workspaceRoot) containsResolvedPath(evaluated string) bool {
	return isInsideRoot(w.resolvedRoot, evaluated)
}

// reportDisplayPath renders a resolved path relative to the workspace with forward slashes,
// so tool output reads the same on every OS and can be passed straight back as a path argument.
func (w workspaceRoot) reportDisplayPath(evaluated, fallback string) string {
	rel, err := filepath.Rel(w.resolvedRoot, evaluated)
	if err != nil {
		return fallback
	}
	return filepath.ToSlash(rel)
}

func isInsideRoot(root, target string) bool {
	rel, err := filepath.Rel(root, target)
	if err != nil || filepath.IsAbs(rel) {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
