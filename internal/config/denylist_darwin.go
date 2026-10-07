//go:build darwin

package config

func appendPlatformDenyGlobs(lookup envLookup, home string, globs *DenyGlobs) {
	for _, rel := range []string{
		"Library/Keychains/**",
		"Library/Application Support/Google/Chrome/**",
		"Library/Application Support/Firefox/**",
		"Library/Safari/**",
		"Library/Caches/TemporaryItems/travis.sock",
	} {
		globs.Read = append(globs.Read, buildRootedGlob(home, rel))
	}
	globs.Write = append(globs.Write, buildRootedGlob(home, "Library/LaunchAgents/**"))
}
