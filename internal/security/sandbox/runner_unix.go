//go:build linux || darwin

package sandbox

import (
	"os"
	"path/filepath"
	"strings"
)

// ResolveRunnerReadPaths reports what a sandboxed executable must be able to read to start.
// A runner in <prefix>/bin needs its whole prefix (node loads libraries from it and is exec'd through
// bin symlinks), but never the home folder or anything above it, which would expose the user's files.
func ResolveRunnerReadPaths(executable string) []string {
	dir := filepath.Dir(executable)
	candidates := []string{dir, executable}
	if filepath.Base(dir) == "bin" {
		candidates = append([]string{filepath.Dir(dir)}, candidates...)
	}
	home, err := os.UserHomeDir()
	for _, candidate := range candidates {
		if !isRootDir(candidate) && (err != nil || !isSameOrAncestor(candidate, home)) {
			return []string{candidate}
		}
	}
	return []string{executable}
}

func isRootDir(path string) bool {
	return filepath.Dir(path) == path
}

func isSameOrAncestor(dir, path string) bool {
	rel, err := filepath.Rel(dir, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
