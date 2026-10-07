package config

import (
	"os"
	"path/filepath"
	"strings"
)

// DenyGlobs holds path patterns that are always denied for read/write, and overrides any grant 
type DenyGlobs struct {
	Read  []string
	Write []string
}

func BuildDenyGlobs(paths PathSet, home string) DenyGlobs {
	return buildDenyGlobsFromInputs(os.LookupEnv, paths, home)
}

func buildDenyGlobsFromInputs(lookup envLookup, paths PathSet, home string) DenyGlobs {
	var globs DenyGlobs
	for _, rel := range []string{
		".ssh/**", ".aws/**", ".azure/**", ".config/gcloud/**", ".kube/**", ".gnupg/**",
		".npmrc", ".pypirc", ".netrc", ".git-credentials", ".docker/config.json",
	} {
		globs.Read = append(globs.Read, buildRootedGlob(home, rel))
	}
	globs.Read = append(globs.Read,
		"**/.env",
		"**/.env.*",
		buildRootedGlob(paths.StateDir, "**"),
	)

	for _, rel := range []string{".bashrc", ".zshrc", ".profile", ".config/fish/**", ".ssh/authorized_keys"} {
		globs.Write = append(globs.Write, buildRootedGlob(home, rel))
	}
	globs.Write = append(globs.Write, "**/.travis/**", "**/.git/hooks/**")

	appendPlatformDenyGlobs(lookup, home, &globs)
	globs.Write = append(globs.Write, globs.Read...)
	return globs
}

// joins the root and pattern into one path, accounting for special characters in the root
func buildRootedGlob(root, relGlob string) string {
	escapedRoot := globMetaEscaper.Replace(filepath.ToSlash(filepath.Clean(root)))
	return strings.TrimSuffix(escapedRoot, "/") + "/" + relGlob
}

var globMetaEscaper = strings.NewReplacer(`\`, `\\`, `*`, `\*`, `?`, `\?`, `[`, `\[`, `]`, `\]`, `{`, `\{`, `}`, `\}`)
