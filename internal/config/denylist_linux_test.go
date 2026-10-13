//go:build linux

package config

import (
	"path/filepath"
	"testing"
)

func TestBuildDenyGlobsLinuxMatches(t *testing.T) {
	root := filepath.ToSlash(t.TempDir())
	home := root + "/home"
	runtimeDir := root + "/run/user/1000"
	lookup := buildEnvLookupFromMap(map[string]string{"XDG_RUNTIME_DIR": filepath.FromSlash(runtimeDir)})
	globs := buildDenyGlobsFromInputs(lookup, PathSet{StateDir: filepath.FromSlash(root + "/state")}, filepath.FromSlash(home))

	tests := []struct {
		target                      string
		isReadDenied, isWriteDenied bool
	}{
		{target: home + "/.local/share/keyrings/login.keyring", isReadDenied: true, isWriteDenied: true},
		{target: home + "/.mozilla/firefox/x/logins.json", isReadDenied: true, isWriteDenied: true},
		{target: home + "/.config/google-chrome/Default/Cookies", isReadDenied: true, isWriteDenied: true},
		{target: home + "/.config/chromium/Default/Cookies", isReadDenied: true, isWriteDenied: true},
		{target: runtimeDir + "/travis-1234.sock", isReadDenied: true, isWriteDenied: true},
		{target: home + "/.config/autostart/evil.desktop", isWriteDenied: true},
		{target: home + "/.config/systemd/user/evil.service", isWriteDenied: true},
		{target: "/var/spool/cron/crontabs/user", isWriteDenied: true},
		{target: runtimeDir + "/other.sock"},
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

func TestBuildDenyGlobsLinuxIgnoresRelativeRuntimeDir(t *testing.T) {
	home := t.TempDir()
	unset := buildDenyGlobsFromInputs(buildEnvLookupFromMap(nil), PathSet{}, home)
	relative := buildDenyGlobsFromInputs(buildEnvLookupFromMap(map[string]string{"XDG_RUNTIME_DIR": "run/user"}), PathSet{}, home)

	if len(relative.Read) != len(unset.Read) {
		t.Fatalf("relative XDG_RUNTIME_DIR added read-deny globs: %v", relative.Read)
	}
}
