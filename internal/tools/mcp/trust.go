package mcp

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"time"

	"github.com/pelletier/go-toml/v2"

	"github.com/Kzeezee/travis/internal/core"
)

const (
	trustFileName = "mcp-trust.toml"
	trustDirMode  = 0o700
	trustFileMode = 0o600
)

// trustEntry is one user's "always allow" answer for one exact server launch — separate
// from core.DecisionRule, which is shaped for a single glob Scope string, not a
// (command, []args) tuple.
type trustEntry struct {
	Command string
	Args    []string
	// The sandbox grant the launch was approved under; entries from before 4E have none and match nothing.
	Tier         string
	AllowNetwork bool     `toml:"allow_network"`
	ReadPaths    []string `toml:"read_paths"`
	WritePaths   []string `toml:"write_paths"`
	AddedAt      time.Time
	Source       string
}

func (e trustEntry) matches(command string, args []string, grant launchGrant) bool {
	return e.Command == command && slices.Equal(e.Args, args) && e.Tier == string(grant.Tier) &&
		e.AllowNetwork == grant.AllowNetwork && slices.Equal(e.ReadPaths, grant.ReadPaths) && slices.Equal(e.WritePaths, grant.WritePaths)
}

// trustFile is one append-worthy TOML document, same convention as decisions.go's
// decisionRuleFile: a single [[entry]] table, so repeated appends accumulate into a
// valid array of tables.
type trustFile struct {
	Entry []trustEntry `toml:"entry"`
}

// trustStore remembers which exact MCP server launches a human has already approved
// "always", persisted at path so future `travis chat` sessions don't re-ask — the
// user-scoped equivalent of decisions.toml, kept separate because [mcp.servers] is
// itself user-config-only (R-27) and applies across every workspace, not per-folder.
// A load failure or a corrupted file is treated as empty, never as a reason to fail
// the session: trust fails safe toward asking again, not toward crashing.
type trustStore struct {
	path string

	mu      sync.Mutex
	entries []trustEntry
}

// buildTrustStore loads path if it exists; a missing or unparsable file starts empty.
func buildTrustStore(path string) *trustStore {
	store := &trustStore{path: path}
	data, err := os.ReadFile(path)
	if err != nil {
		return store
	}
	var file trustFile
	if err := toml.Unmarshal(data, &file); err != nil {
		return store
	}
	store.entries = file.Entry
	return store
}

// isTrusted reports whether command, args and sandbox grant exactly match a previously approved entry.
func (s *trustStore) isTrusted(command string, args []string, grant launchGrant) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.ContainsFunc(s.entries, func(entry trustEntry) bool { return entry.matches(command, args, grant) })
}

// add appends one trusted entry, both in memory and durably on disk under an exclusive
// lock (same D-32 reasoning as AddDecisionRule), so a concurrent writer never corrupts
// the file and a crash right after never loses the in-memory half of the picture.
func (s *trustStore) add(command string, args []string, grant launchGrant, source string, clock core.Clock) error {
	entry := trustEntry{
		Command: command, Args: slices.Clone(args),
		Tier: string(grant.Tier), AllowNetwork: grant.AllowNetwork, ReadPaths: slices.Clone(grant.ReadPaths), WritePaths: slices.Clone(grant.WritePaths),
		AddedAt: clock.ReportCurrentTime().UTC(), Source: source,
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	file, err := openTrustFile(s.path)
	if err != nil {
		return fmt.Errorf("mcp trust: %w", err)
	}
	defer file.Close()

	if err := lockExclusive(file); err != nil {
		return fmt.Errorf("mcp trust: lock: %w", err)
	}
	defer func() { _ = unlockExclusive(file) }()

	if _, err := file.Seek(0, io.SeekEnd); err != nil {
		return fmt.Errorf("mcp trust: seek: %w", err)
	}
	encoded, err := toml.Marshal(trustFile{Entry: []trustEntry{entry}})
	if err != nil {
		return fmt.Errorf("mcp trust: encode: %w", err)
	}
	if _, err := file.Write(encoded); err != nil {
		return fmt.Errorf("mcp trust: write: %w", err)
	}
	if err := file.Sync(); err != nil {
		return err
	}
	s.entries = append(s.entries, entry)
	return nil
}

// openTrustFile opens the trust file without following a symlink at its directory or at
// the file itself — the same R-26-style reasoning as decisions.go's openDecisionsFile: a
// planted symlink must never redirect a trusted write somewhere else on disk.
func openTrustFile(path string) (*os.File, error) {
	dir := filepath.Dir(path)
	if err := os.Mkdir(dir, trustDirMode); err != nil && !errors.Is(err, fs.ErrExist) {
		return nil, err
	}
	if info, err := os.Lstat(dir); err != nil {
		return nil, err
	} else if !info.IsDir() {
		return nil, fmt.Errorf("%q is not a real folder: %w", dir, core.ErrDenied)
	}

	file, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE|os.O_EXCL, trustFileMode)
	if !errors.Is(err, fs.ErrExist) {
		return file, err
	}
	existing, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !existing.Mode().IsRegular() {
		return nil, fmt.Errorf("%q is not a regular file: %w", path, core.ErrDenied)
	}
	if file, err = os.OpenFile(path, os.O_RDWR, 0); err != nil {
		return nil, err
	}
	if opened, err := file.Stat(); err != nil || !os.SameFile(existing, opened) {
		_ = file.Close()
		return nil, fmt.Errorf("%q changed while it was being opened: %w", path, core.ErrDenied)
	}
	return file, nil
}
