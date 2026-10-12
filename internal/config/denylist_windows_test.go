//go:build windows

package config

import (
	"path/filepath"
	"testing"
)

func TestBuildDenyGlobsWindowsMatches(t *testing.T) {
	root := filepath.ToSlash(t.TempDir())
	home := root + "/home"
	appData := root + "/roaming"
	lookup := buildEnvLookupFromMap(map[string]string{"AppData": filepath.FromSlash(appData)})
	globs := buildDenyGlobsFromInputs(lookup, PathSet{StateDir: filepath.FromSlash(root + "/state")}, filepath.FromSlash(home))

	tests := []struct {
		target                      string
		isReadDenied, isWriteDenied bool
	}{
		{target: appData + "/Microsoft/Protect/S-1-5/key", isReadDenied: true, isWriteDenied: true},
		{target: appData + "/Microsoft/Credentials/blob", isReadDenied: true, isWriteDenied: true},
		{target: appData + "/Mozilla/Firefox/Profiles/x/logins.json", isReadDenied: true, isWriteDenied: true},
		{target: home + "/AppData/Local/Google/Chrome/User Data/Default/Cookies", isReadDenied: true, isWriteDenied: true},
		{target: home + "/AppData/Local/Microsoft/Edge/User Data/Default/Cookies", isReadDenied: true, isWriteDenied: true},
		{target: appData + "/Microsoft/Windows/Start Menu/Programs/Startup/evil.lnk", isWriteDenied: true},
		{target: home + "/AppData/Roaming/Microsoft/Protect/S-1-5/key"},
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
