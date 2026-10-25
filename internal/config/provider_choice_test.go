package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadStartsOnTheRememberedProviderChoice(t *testing.T) {
	pathSet := testPathSet()
	pathSet.StateDir = "/state"
	choiceFile := filepath.Join(pathSet.StateDir, providerChoiceFileName)
	noEnv := func(string) (string, bool) { return "", false }

	cases := []struct {
		name, remembered, env, want string
	}{
		{name: "a remembered profile beats the config file", remembered: "gemini\n", want: "gemini"},
		{name: "a profile that no longer exists is ignored", remembered: "gone", want: "cloud"},
		{name: "the env var still beats the remembered profile", remembered: "gemini", env: "deepseek", want: "deepseek"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			lookup := noEnv
			if tc.env != "" {
				lookup = func(key string) (string, bool) { return tc.env, key == envProviderActive }
			}
			readFile := buildFileReaderFromMap(map[string]string{choiceFile: tc.remembered})

			cfg, _, err := load(readFile, pathSet, nil, lookup)
			if err != nil {
				t.Fatal(err)
			}
			if cfg.Provider.Active != tc.want {
				t.Fatalf("got active %q, want %q", cfg.Provider.Active, tc.want)
			}
		})
	}
}

func TestRecordProviderChoiceIsReadBackByLoad(t *testing.T) {
	pathSet := testPathSet()
	pathSet.StateDir = filepath.Join(t.TempDir(), "state")

	if err := RecordProviderChoice(pathSet, "deepseek"); err != nil {
		t.Fatal(err)
	}
	cfg, _, err := load(readRealChoiceFile(pathSet), pathSet, nil, func(string) (string, bool) { return "", false })
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Provider.Active != "deepseek" {
		t.Fatalf("got active %q, want the recorded deepseek", cfg.Provider.Active)
	}
}

// readRealChoiceFile reads only the choice file from disk, so config files still come from nowhere.
func readRealChoiceFile(pathSet PathSet) fileReader {
	choiceFile := filepath.Join(pathSet.StateDir, providerChoiceFileName)
	fake := buildFileReaderFromMap(nil)
	return func(name string) ([]byte, error) {
		if name == choiceFile {
			return os.ReadFile(name)
		}
		return fake(name)
	}
}
