//go:build windows

package config

import "path/filepath"

func buildPlatformPathDefaultsForOS(lookup envLookup, home string) (platformPathDefaults, error) {
	appData := pathFromEnvOrFallback(lookup, "AppData", filepath.Join(home, "AppData", "Roaming"))
	localAppData := pathFromEnvOrFallback(lookup, "LocalAppData", filepath.Join(home, "AppData", "Local"))
	programData := pathFromEnvOrFallback(lookup, "ProgramData", filepath.Join(filepath.VolumeName(home)+string(filepath.Separator), "ProgramData"))

	return platformPathDefaults{
		configDir:        filepath.Join(appData, "travis"),
		stateDir:         filepath.Join(localAppData, "travis"),
		cacheDir:         filepath.Join(localAppData, "travis", "cache"),
		systemConfigFile: filepath.Join(programData, "travis", "config.toml"),
	}, nil
}
