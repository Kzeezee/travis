// Package filestore keeps one session's log as an append-only JSON-lines file, synced to disk
// on every append (DESIGN.md §5.15). Only the broker process writes it.
package filestore

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sync"

	"github.com/Kzeezee/travis/internal/core"
	"github.com/Kzeezee/travis/internal/state/session"
)

// Session logs hold conversation text and can hold secrets, so only the user may read them.
const (
	logDirMode  = 0o700
	logFileMode = 0o600
)

type Store struct {
	mu      sync.Mutex
	file    *os.File // <dir>/<sessionID>.jsonl, O_APPEND
	entries []core.SessionEntry
}

// Open creates the log directory and replays <dir>/<sessionID>.jsonl, dropping a torn last line.
func Open(dir string, sessionID core.ID) (*Store, error) {
	if sessionID == "" || filepath.Base(string(sessionID)) != string(sessionID) {
		return nil, fmt.Errorf("session log: invalid session id %q", sessionID)
	}
	if err := os.MkdirAll(dir, logDirMode); err != nil {
		return nil, fmt.Errorf("session log: create dir: %w", err)
	}
	path := filepath.Join(dir, string(sessionID)+".jsonl")
	entries, err := replayLog(path)
	if err != nil {
		return nil, err
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, logFileMode)
	if err != nil {
		return nil, fmt.Errorf("session log: open: %w", err)
	}
	return &Store{file: file, entries: entries}, nil
}

func (s *Store) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.file.Close()
}

// Append writes the entries as one write and syncs it; when it returns nil they are on disk.
// Every entry is validated first, so a bad one leaves the log untouched.
func (s *Store) Append(entries ...core.SessionEntry) error {
	var lines bytes.Buffer
	for _, entry := range entries {
		if err := session.ValidateEntry(entry); err != nil {
			return err
		}
		line, err := json.Marshal(entry)
		if err != nil {
			return fmt.Errorf("session log: encode entry: %w", err)
		}
		lines.Write(line)
		lines.WriteByte('\n')
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := s.file.Write(lines.Bytes()); err != nil {
		return fmt.Errorf("session log: append: %w", err)
	}
	if err := s.file.Sync(); err != nil {
		return fmt.Errorf("session log: sync: %w", err)
	}
	s.entries = append(s.entries, entries...)
	return nil
}

// ReadEntries returns a copy of every entry, in the order they were appended.
func (s *Store) ReadEntries() []core.SessionEntry {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.entries)
}

// replayLog drops a torn tail but rejects any corrupt complete line.
func replayLog(path string) ([]core.SessionEntry, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("session log: read: %w", err)
	}
	var entries []core.SessionEntry
	validLen := 0
	for rest := data; len(rest) > 0; {
		end := bytes.IndexByte(rest, '\n')
		if end < 0 {
			break
		}
		entry, err := decodeEntry(rest[:end])
		if err != nil {
			return nil, fmt.Errorf("session log: corrupt line at byte %d: %w", validLen, err)
		}
		entries = append(entries, entry)
		validLen += end + 1
		rest = rest[end+1:]
	}
	if validLen < len(data) {
		if err := os.Truncate(path, int64(validLen)); err != nil {
			return nil, fmt.Errorf("session log: drop torn tail: %w", err)
		}
	}
	return entries, nil
}

func decodeEntry(line []byte) (core.SessionEntry, error) {
	decoder := json.NewDecoder(bytes.NewReader(line))
	decoder.DisallowUnknownFields()
	var entry core.SessionEntry
	if err := decoder.Decode(&entry); err != nil {
		return core.SessionEntry{}, err
	}
	return entry, session.ValidateEntry(entry)
}
