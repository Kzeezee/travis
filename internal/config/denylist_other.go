//go:build !linux && !darwin && !windows

package config

func appendPlatformDenyGlobs(lookup envLookup, home string, globs *DenyGlobs) {}
