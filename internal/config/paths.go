package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/Kzeezee/travis/internal/core"
)

type PathSet struct {
	ConfigDir string
	StateDir  string
	CacheDir  string
	// RuntimeDir holds per-session broker sockets. It is OS-chosen and short, even under
	// TRAVIS_HOME, because Unix socket paths are limited to about 104 bytes.
	RuntimeDir          string
	SystemConfigFile    string
	WorkspaceDir        string
	WorkspaceConfigFile string
}

type envLookup func(string) (string, bool)

type platformPathDefaults struct {
	configDir        string
	stateDir         string
	cacheDir         string
	systemConfigFile string
}

func BuildCurrentPathSet() (PathSet, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return PathSet{}, fmt.Errorf("home directory: %w", err)
	}
	cwd, err := os.Getwd()
	if err != nil {
		return PathSet{}, fmt.Errorf("workspace directory: %w", err)
	}
	return buildPathSetFromInputs(os.LookupEnv, home, cwd)
}

func buildPathSetFromInputs(lookup envLookup, home, cwd string) (PathSet, error) {
	paths, err := buildPersistentPathSet(lookup, home, cwd)
	if err != nil {
		return PathSet{}, err
	}
	if paths.RuntimeDir, err = cleanAbsolutePath("runtime directory", selectRuntimeDir(lookup)); err != nil {
		return PathSet{}, err
	}
	return paths, nil
}

// selectRuntimeDir prefers the per-user XDG runtime dir, which Linux sets; elsewhere it is the
// temp dir, which macOS makes private to the user and Windows pipes never use. The fallback is
// the temp dir itself, not a shared subdirectory another user could create first.
func selectRuntimeDir(lookup envLookup) string {
	if runtimeRoot := readTrimmedEnvValue(lookup, "XDG_RUNTIME_DIR"); runtimeRoot != "" {
		return filepath.Join(filepath.Clean(normalizeConfigPath(runtimeRoot)), "travis")
	}
	return os.TempDir()
}

func buildPersistentPathSet(lookup envLookup, home, cwd string) (PathSet, error) {
	workspaceDir, err := cleanAbsolutePath("workspace directory", cwd)
	if err != nil {
		return PathSet{}, err
	}

	travisHome := readTrimmedEnvValue(lookup, "TRAVIS_HOME")
	if travisHome != "" {
		travisHome, err = cleanAbsolutePath("TRAVIS_HOME", travisHome)
		if err != nil {
			return PathSet{}, err
		}
		return buildValidatedPathSet(
			travisHome,
			filepath.Join(travisHome, "state"),
			filepath.Join(travisHome, "cache"),
			filepath.Join(travisHome, "config.toml"),
			workspaceDir,
		)
	}

	// If TRAVIS_HOME is not found in the environment variables, or is found but
	// empty/only spaces, Travis builds its default absolute paths using the OS rules.
	home, err = cleanAbsolutePath("home directory", home)
	if err != nil {
		return PathSet{}, err
	}
	defaults, err := buildPlatformPathDefaultsForOS(lookup, home)
	if err != nil {
		return PathSet{}, err
	}
	return buildValidatedPathSet(defaults.configDir, defaults.stateDir, defaults.cacheDir, defaults.systemConfigFile, workspaceDir)
}

func buildValidatedPathSet(configDir, stateDir, cacheDir, systemConfigFile, workspaceDir string) (PathSet, error) {
	var err error
	if configDir, err = cleanAbsolutePath("config directory", configDir); err != nil {
		return PathSet{}, err
	}
	if stateDir, err = cleanAbsolutePath("state directory", stateDir); err != nil {
		return PathSet{}, err
	}
	if cacheDir, err = cleanAbsolutePath("cache directory", cacheDir); err != nil {
		return PathSet{}, err
	}
	if systemConfigFile, err = cleanAbsolutePath("system config file", systemConfigFile); err != nil {
		return PathSet{}, err
	}

	return PathSet{
		ConfigDir:           filepath.Clean(configDir),
		StateDir:            filepath.Clean(stateDir),
		CacheDir:            filepath.Clean(cacheDir),
		SystemConfigFile:    filepath.Clean(systemConfigFile),
		WorkspaceDir:        filepath.Clean(workspaceDir),
		WorkspaceConfigFile: filepath.Join(workspaceDir, ".travis", "config.toml"),
	}, nil
}

func (p PathSet) Resolve(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", fmt.Errorf("path is empty: %w", core.ErrInvalid)
	}

	normalizedPath := normalizeConfigPath(value)
	if filepath.IsAbs(normalizedPath) {
		return filepath.Clean(normalizedPath), nil
	}

	prefixes := map[string]string{
		"config":    p.ConfigDir,
		"state":     p.StateDir,
		"cache":     p.CacheDir,
		"workspace": p.WorkspaceDir,
	}
	for prefix, root := range prefixes {
		marker := prefix + ":"
		if strings.HasPrefix(normalizedPath, marker) {
			return filepath.Join(root, strings.TrimPrefix(normalizedPath, marker)), nil
		}
	}

	if prefix, _, ok := strings.Cut(normalizedPath, ":"); ok && prefix != "" && !isDriveLetter(prefix) {
		return "", fmt.Errorf("unknown path prefix %q: %w", prefix, core.ErrInvalid)
	}
	return filepath.Join(p.WorkspaceDir, normalizedPath), nil
}

func readTrimmedEnvValue(lookup envLookup, envKey string) string {
	value, ok := lookup(envKey)
	if !ok {
		return ""
	}
	return strings.TrimSpace(value)
}

func cleanAbsolutePath(name, value string) (string, error) {
	value = filepath.Clean(normalizeConfigPath(strings.TrimSpace(value)))
	if value == "." || value == "" {
		return "", fmt.Errorf("%s is empty: %w", name, core.ErrInvalid)
	}
	if !filepath.IsAbs(value) {
		return "", fmt.Errorf("%s %q is not absolute: %w", name, value, core.ErrInvalid)
	}
	return value, nil
}

func normalizeConfigPath(value string) string {
	return filepath.FromSlash(strings.ReplaceAll(value, "\\", "/"))
}

func isDriveLetter(prefix string) bool {
	if len(prefix) != 1 {
		return false
	}
	letter := prefix[0]
	return ('A' <= letter && letter <= 'Z') || ('a' <= letter && letter <= 'z')
}
