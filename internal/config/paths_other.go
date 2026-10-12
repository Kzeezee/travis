//go:build !linux && !darwin && !windows

package config

import "path/filepath"

func buildPlatformPathDefaultsForOS(lookup envLookup, home string) (platformPathDefaults, error) {
	return platformPathDefaults{
		configDir:        filepath.Join(home, ".config", "travis"),
		stateDir:         filepath.Join(home, ".local", "state", "travis"),
		cacheDir:         filepath.Join(home, ".cache", "travis"),
		systemConfigFile: filepath.Join(home, ".config", "travis", "config.toml"),
	}, nil
}
