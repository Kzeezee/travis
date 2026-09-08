//go:build linux

package config

import (
	"os"
	"path/filepath"
)

func buildPlatformPathDefaultsForOS(lookup envLookup, home string) (platformPathDefaults, error) {
	configRoot := pathFromEnvOrFallback(lookup, "XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	stateRoot := pathFromEnvOrFallback(lookup, "XDG_STATE_HOME", filepath.Join(home, ".local", "state"))
	cacheRoot := pathFromEnvOrFallback(lookup, "XDG_CACHE_HOME", filepath.Join(home, ".cache"))
	systemRoot := string(os.PathSeparator)

	return platformPathDefaults{
		configDir:        filepath.Join(configRoot, "travis"),
		stateDir:         filepath.Join(stateRoot, "travis"),
		cacheDir:         filepath.Join(cacheRoot, "travis"),
		systemConfigFile: filepath.Join(systemRoot, "etc", "travis", "config.toml"),
	}, nil
}
