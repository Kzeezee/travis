package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// providerChoiceFileName holds the profile /provider last switched to. It is state, not config:
// travis writes it, so the user's own config file is never rewritten.
const providerChoiceFileName = "provider-choice"

// RecordProviderChoice remembers profileName as the provider the next session starts on.
func RecordProviderChoice(paths PathSet, profileName string) error {
	if err := os.MkdirAll(paths.StateDir, 0o700); err != nil {
		return fmt.Errorf("create state directory: %w", err)
	}
	path := filepath.Join(paths.StateDir, providerChoiceFileName)
	if err := os.WriteFile(path, []byte(profileName+"\n"), 0o600); err != nil {
		return fmt.Errorf("remember provider choice: %w", err)
	}
	return nil
}

// applyProviderChoice sits above the config files and below env vars and flags. A choice naming
// a profile that no longer exists is ignored, so a deleted profile never stops travis starting.
func applyProviderChoice(cfg *Config, readFile fileReader, paths PathSet) Diagnostics {
	path := filepath.Join(paths.StateDir, providerChoiceFileName)
	raw, err := readFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return Diagnostics{{Severity: SeverityWarning, Message: fmt.Sprintf("read %s: %v", path, err)}}
	}
	name := strings.TrimSpace(string(raw))
	if _, ok := cfg.Provider.Profiles[name]; !ok {
		return nil
	}
	cfg.Provider.Active = name
	return nil
}
