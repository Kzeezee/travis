//go:build linux || windows

package config

import "path/filepath"

func selectPathFromEnvOrFallback(lookup envLookup, envKey, fallbackPath string) string {
	if value := readTrimmedEnvValue(lookup, envKey); value != "" {
		return filepath.Clean(normalizeConfigPath(value))
	}
	return fallbackPath
}
