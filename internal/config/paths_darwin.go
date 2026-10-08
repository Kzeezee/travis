//go:build darwin

package config

import (
	"os"
	"path/filepath"
)

func buildPlatformPathDefaultsForOS(lookup envLookup, home string) (platformPathDefaults, error) {
	systemRoot := string(os.PathSeparator)

	return platformPathDefaults{
		configDir:        filepath.Join(home, "Library", "Application Support", "travis"),
		stateDir:         filepath.Join(home, "Library", "Application Support", "travis"),
		cacheDir:         filepath.Join(home, "Library", "Caches", "travis"),
		systemConfigFile: filepath.Join(systemRoot, "etc", "travis", "config.toml"),
	}, nil
}

// selectRuntimeDirForOS uses $TMPDIR, which macOS already makes private to the user.
func selectRuntimeDirForOS(envLookup) string {
	return os.TempDir()
}
