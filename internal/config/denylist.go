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
	// .git/config can set core.hooksPath or core.fsmonitor, the same code-on-next-git-run hole.
	globs.Write = append(globs.Write, "**/.travis/**", "**/.git/hooks/**", "**/.git/config")
	// User config grants exec_allow and trusted skills; the model must never write it, even
	// when the workspace contains it (travis run from $HOME).
	if paths.ConfigDir != "" {
		globs.Write = append(globs.Write, buildRootedGlob(paths.ConfigDir, "**"))
	}
	if paths.SystemConfigFile != "" {
		globs.Write = append(globs.Write, globMetaEscaper.Replace(filepath.ToSlash(filepath.Clean(paths.SystemConfigFile))))
	}

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
