package mcp

import (
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/pelletier/go-toml/v2"

	"github.com/Kzeezee/travis/internal/core"
	"github.com/Kzeezee/travis/internal/security/sandbox"
)

func createSymlinkOrSkip(t *testing.T, target, link string) {
	t.Helper()
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlinks unavailable here: %v", err)
	}
}

func writeFile(t *testing.T, path, content string) string {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// readTrustEntries parses path through the real trustFile shape, independent of the
// package's own unexported load path, so a test asserts on-disk content, not round-trips
// through buildTrustStore.
func readTrustEntries(t *testing.T, path string) []trustEntry {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var file trustFile
	if err := toml.Unmarshal(data, &file); err != nil {
		t.Fatalf("%s did not parse: %v\n%s", path, err, data)
	}
	return file.Entry
}

// testGrant is a sandboxed launch with no extra grants, the shape most trust entries take.
var testGrant = launchGrant{Tier: sandbox.TierSeatbelt}

// Entries written before 4E carry no tier, so each server asks once more rather than inheriting trust unsandboxed.
func TestTrustStoreIgnoresEntriesWithoutATier(t *testing.T) {
	path := filepath.Join(t.TempDir(), trustFileName)
	writeFile(t, path, "[[entry]]\nCommand = 'npx'\nArgs = ['x']\nSource = 'approval modal'\n")
	store := buildTrustStore(path)
	for _, grant := range []launchGrant{testGrant, {Tier: sandbox.TierNone}} {
		if store.isTrusted("npx", []string{"x"}, grant) {
			t.Fatalf("a pre-sandbox entry is trusted for %+v", grant)
		}
	}
}

func TestTrustStoreAddWritesAParseableEntry(t *testing.T) {
	path := filepath.Join(t.TempDir(), trustFileName)
	store := buildTrustStore(path)
	clock := core.FixedClock{Time: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}

	if err := store.add("npx", []string{"-y", "@modelcontextprotocol/server-filesystem"}, testGrant, "approval modal", clock); err != nil {
		t.Fatal(err)
	}

	entries := readTrustEntries(t, path)
	if len(entries) != 1 || entries[0].Command != "npx" {
		t.Fatalf("entries = %+v, want one entry for npx", entries)
	}
	if entries[0].AddedAt.IsZero() {
		t.Fatal("add did not stamp AddedAt")
	}
}

func TestTrustStoreIsTrustedMatchesExactCommandAndArgs(t *testing.T) {
	store := buildTrustStore(filepath.Join(t.TempDir(), trustFileName))
	clock := core.FixedClock{Time: time.Now()}
	if err := store.add("npx", []string{"-y", "server-filesystem"}, testGrant, "test", clock); err != nil {
		t.Fatal(err)
	}

	args := []string{"-y", "server-filesystem"}
	cases := map[string]struct {
		command string
		args    []string
		grant   launchGrant
		want    bool
	}{
		"exact match":       {"npx", args, testGrant, true},
		"different args":    {"npx", []string{"-y", "server-github"}, testGrant, false},
		"different command": {"uvx", args, testGrant, false},
		"extra arg":         {"npx", []string{"-y", "server-filesystem", "extra"}, testGrant, false},
		// A tier change or a widened grant is a different launch, so it asks again (4E).
		"different tier":     {"npx", args, launchGrant{Tier: sandbox.TierNone}, false},
		"network widened":    {"npx", args, launchGrant{Tier: testGrant.Tier, AllowNetwork: true}, false},
		"read path widened":  {"npx", args, launchGrant{Tier: testGrant.Tier, ReadPaths: []string{"/srv"}}, false},
		"write path widened": {"npx", args, launchGrant{Tier: testGrant.Tier, WritePaths: []string{"/srv"}}, false},
	}
	for name, tt := range cases {
		t.Run(name, func(t *testing.T) {
			if got := store.isTrusted(tt.command, tt.args, tt.grant); got != tt.want {
				t.Fatalf("isTrusted(%q, %v) = %v, want %v", tt.command, tt.args, got, tt.want)
			}
		})
	}
}

func TestTrustStoreAccumulatesAcrossCalls(t *testing.T) {
	path := filepath.Join(t.TempDir(), trustFileName)
	store := buildTrustStore(path)
	clock := core.FixedClock{Time: time.Now()}

	if err := store.add("npx", []string{"a"}, testGrant, "", clock); err != nil {
		t.Fatal(err)
	}
	if err := store.add("npx", []string{"b"}, testGrant, "", clock); err != nil {
		t.Fatal(err)
	}

	if entries := readTrustEntries(t, path); len(entries) != 2 {
		t.Fatalf("entries = %+v, want 2", entries)
	}
}

// TestTrustStoreSerializesConcurrentWriters proves the exclusive lock (D-32, same
// reasoning as decisions.go): N goroutines appending at once must still leave N
// well-formed entries, never a corrupted interleaving of two writes.
func TestTrustStoreSerializesConcurrentWriters(t *testing.T) {
	path := filepath.Join(t.TempDir(), trustFileName)
	store := buildTrustStore(path)
	const writers = 8

	var wg sync.WaitGroup
	for i := 0; i < writers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			clock := core.FixedClock{Time: time.Now()}
			if err := store.add("npx", []string{"writer"}, testGrant, "concurrent", clock); err != nil {
				t.Error(err)
			}
		}(i)
	}
	wg.Wait()

	if entries := readTrustEntries(t, path); len(entries) != writers {
		t.Fatalf("entries has %d rows, want %d: %+v", len(entries), writers, entries)
	}
}

// TestTrustStoreRefusesPlantedSymlinks mirrors
// TestLocalBrokerAddDecisionRuleRefusesPlantedSymlinks: a cloned repo or compromised
// process could plant a symlink aimed at a file outside travis's state dir, hoping an
// append lands there instead.
func TestTrustStoreRefusesPlantedSymlinks(t *testing.T) {
	for name, plant := range map[string]func(t *testing.T, stateDir, outside string) string{
		"state dir is a link": func(t *testing.T, stateDir, outside string) string {
			// buildTrustStore is handed the full file path; simulate a symlinked parent
			// by making the directory component of that path a link.
			createSymlinkOrSkip(t, outside, stateDir)
			return filepath.Join(stateDir, trustFileName)
		},
		"trust file is a link": func(t *testing.T, stateDir, outside string) string {
			if err := os.MkdirAll(stateDir, 0o700); err != nil {
				t.Fatal(err)
			}
			target := filepath.Join(outside, trustFileName)
			createSymlinkOrSkip(t, target, filepath.Join(stateDir, trustFileName))
			return filepath.Join(stateDir, trustFileName)
		},
	} {
		t.Run(name, func(t *testing.T) {
			root, outside := t.TempDir(), t.TempDir()
			stateDir := filepath.Join(root, "state")
			planted := writeFile(t, filepath.Join(outside, trustFileName), "planted\n")
			path := plant(t, stateDir, outside)

			store := buildTrustStore(path)
			clock := core.FixedClock{Time: time.Now()}
			err := store.add("npx", []string{"x"}, testGrant, "test", clock)
			if !errors.Is(err, core.ErrDenied) {
				t.Fatalf("add through a planted link = %v, want ErrDenied", err)
			}
			if content, err := os.ReadFile(planted); err != nil || string(content) != "planted\n" {
				t.Fatalf("the link target now holds %q (%v), want it untouched", content, err)
			}
		})
	}
}

// TestBuildTrustStoreTreatsAMalformedFileAsEmpty is the category decisions.toml never
// needed, because nothing ever reads it back — this store does, so a corrupted file must
// fail safe (re-ask) rather than crash the session.
func TestBuildTrustStoreTreatsAMalformedFileAsEmpty(t *testing.T) {
	path := filepath.Join(t.TempDir(), trustFileName)
	writeFile(t, path, "this is not valid toml {{{")

	store := buildTrustStore(path)
	if store.isTrusted("npx", []string{"x"}, testGrant) {
		t.Fatal("a malformed trust file should never report anything as trusted")
	}
}

func TestBuildTrustStoreWithNoFileYetStartsEmpty(t *testing.T) {
	store := buildTrustStore(filepath.Join(t.TempDir(), trustFileName))
	if store.isTrusted("npx", []string{"x"}, testGrant) {
		t.Fatal("a trust store with no file yet should trust nothing")
	}
}
