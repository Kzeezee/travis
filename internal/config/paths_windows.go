//go:build windows

package config

import (
	"os"
	"path/filepath"
)

func buildPlatformPathDefaultsForOS(lookup envLookup, home string) (platformPathDefaults, error) {
	appData := selectPathFromEnvOrFallback(lookup, "AppData", filepath.Join(home, "AppData", "Roaming"))
	localAppData := selectPathFromEnvOrFallback(lookup, "LocalAppData", filepath.Join(home, "AppData", "Local"))
	programData := selectPathFromEnvOrFallback(lookup, "ProgramData", filepath.Join(filepath.VolumeName(home)+string(filepath.Separator), "ProgramData"))

	return platformPathDefaults{
		configDir:        filepath.Join(appData, "travis"),
		stateDir:         filepath.Join(localAppData, "travis"),
		cacheDir:         filepath.Join(localAppData, "travis", "cache"),
		systemConfigFile: filepath.Join(programData, "travis", "config.toml"),
	}, nil
}

// selectRuntimeDirForOS is unused by the broker on Windows, whose pipes have their own namespace.
func selectRuntimeDirForOS(envLookup) string {
	return os.TempDir()
}
