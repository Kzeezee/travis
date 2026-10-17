//go:build linux || darwin

package sandbox

import (
	"os"
	"path/filepath"
	"strings"
)

// prefixDataDir is where Homebrew and Linuxbrew keep service data such as Postgres, MySQL and Redis databases.
const prefixDataDir = "var"

// ResolveRunnerReadPaths reports what a sandboxed executable must be able to read to start.
// A runner in <prefix>/bin needs its prefix (node loads libraries from it and is exec'd through bin symlinks),
// minus the prefix's data folder, and never the home folder or anything above it, which would expose the user's files.
func ResolveRunnerReadPaths(executable string) []string {
	dir := filepath.Dir(executable)
	home, err := os.UserHomeDir()
	isExposingHome := func(path string) bool {
		return isRootDir(path) || err == nil && isSameOrAncestor(path, home)
	}
	if prefix := filepath.Dir(dir); filepath.Base(dir) == "bin" && !isExposingHome(prefix) {
		if paths, err := listPrefixWithoutData(prefix); err == nil {
			return paths
		}
	}
	if !isExposingHome(dir) {
		return []string{dir}
	}
	return []string{executable}
}

// listPrefixWithoutData grants every child of prefix but its data folder. etc stays: node reads its OpenSSL
// config there, though service configs in it can hold passwords too.
func listPrefixWithoutData(prefix string) ([]string, error) {
	entries, err := os.ReadDir(prefix)
	if err != nil {
		return nil, err
	}
	var paths []string
	for _, entry := range entries {
		if entry.Name() != prefixDataDir {
			paths = append(paths, filepath.Join(prefix, entry.Name()))
		}
	}
	return paths, nil
}

func isRootDir(path string) bool {
	return filepath.Dir(path) == path
}

func isSameOrAncestor(dir, path string) bool {
	rel, err := filepath.Rel(dir, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
