//go:build windows

package config

import "path/filepath"

// The broker pipe and Run registry keys are not fs paths; policy denies `\.\` targets and Run keys wait for the Phase 4 sandbox.
func appendPlatformDenyGlobs(lookup envLookup, home string, globs *DenyGlobs) {
	appData := selectPathFromEnvOrFallback(lookup, "AppData", filepath.Join(home, "AppData", "Roaming"))
	localAppData := selectPathFromEnvOrFallback(lookup, "LocalAppData", filepath.Join(home, "AppData", "Local"))

	globs.Read = append(globs.Read,
		buildRootedGlob(appData, "Microsoft/Protect/**"),
		buildRootedGlob(appData, "Microsoft/Credentials/**"),
		buildRootedGlob(appData, "Mozilla/**"),
		buildRootedGlob(localAppData, "Google/Chrome/User Data/**"),
		buildRootedGlob(localAppData, "Microsoft/Edge/User Data/**"),
	)
	globs.Write = append(globs.Write, buildRootedGlob(appData, "Microsoft/Windows/Start Menu/Programs/Startup/**"))
}
