//go:build linux

package config

import (
	"os"
	"path/filepath"
)

func appendPlatformDenyGlobs(lookup envLookup, home string, globs *DenyGlobs) {
	for _, rel := range []string{".local/share/keyrings/**", ".mozilla/**", ".config/google-chrome/**", ".config/chromium/**"} {
		globs.Read = append(globs.Read, buildRootedGlob(home, rel))
	}
	if runtimeDir := readTrimmedEnvValue(lookup, "XDG_RUNTIME_DIR"); filepath.IsAbs(runtimeDir) {
		globs.Read = append(globs.Read, buildRootedGlob(runtimeDir, "travis-*.sock"))
	}

	for _, rel := range []string{".config/autostart/**", ".config/systemd/user/**"} {
		globs.Write = append(globs.Write, buildRootedGlob(home, rel))
	}
	globs.Write = append(globs.Write, buildRootedGlob(filepath.Join(string(os.PathSeparator), "var", "spool", "cron"), "**"))
}
