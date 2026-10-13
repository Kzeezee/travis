//go:build darwin

package config

import (
	"path/filepath"
	"testing"
)

func TestBuildDenyGlobsDarwinMatches(t *testing.T) {
	root := filepath.ToSlash(t.TempDir())
	home := root + "/home"
	globs := buildDenyGlobsFromInputs(buildEnvLookupFromMap(nil), PathSet{StateDir: filepath.FromSlash(root + "/state")}, filepath.FromSlash(home))

	tests := []struct {
		target                      string
		isReadDenied, isWriteDenied bool
	}{
		{target: home + "/Library/Keychains/login.keychain-db", isReadDenied: true, isWriteDenied: true},
		{target: home + "/Library/Application Support/Google/Chrome/Default/Cookies", isReadDenied: true, isWriteDenied: true},
		{target: home + "/Library/Application Support/Firefox/Profiles/x/logins.json", isReadDenied: true, isWriteDenied: true},
		{target: home + "/Library/Safari/History.db", isReadDenied: true, isWriteDenied: true},
		{target: home + "/Library/Caches/TemporaryItems/travis.sock", isReadDenied: true, isWriteDenied: true},
		{target: home + "/Library/LaunchAgents/com.evil.plist", isWriteDenied: true},
		{target: home + "/Library/Application Support/Code/settings.json"},
	}
	for _, tt := range tests {
		if got := matchesAnyGlob(globs.Read, tt.target); got != tt.isReadDenied {
			t.Errorf("read-deny(%q) = %v, want %v", tt.target, got, tt.isReadDenied)
		}
		if got := matchesAnyGlob(globs.Write, tt.target); got != tt.isWriteDenied {
			t.Errorf("write-deny(%q) = %v, want %v", tt.target, got, tt.isWriteDenied)
		}
	}
}
