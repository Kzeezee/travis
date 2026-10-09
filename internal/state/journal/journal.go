// Package journal snapshots files into a content-addressed blob store and logs each change per session, so a turn's writes can be undone (DESIGN.md §5.15).
package journal

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/Kzeezee/travis/internal/core"
)

// Blobs hold file contents and can hold secrets, so only the user may read them.
const (
	journalDirMode  = 0o700
	journalFileMode = 0o600
)

// BlobRef is the hex SHA-256 of a snapshot's bytes; the zero value means "file did not exist".
type BlobRef string

// Change is one journaled write; Before is what Restore puts back.
type Change struct {
	Turn   core.ID `json:"turn"`
	Call   core.ID `json:"call"`
	Path   string  `json:"path"`
	Before BlobRef `json:"before,omitempty"`
	After  BlobRef `json:"after,omitempty"`
}

// Selector picks the changes Restore undoes; v1 selects one whole turn.
type Selector struct{ Turn core.ID }

var ErrIrreversible = errors.New("journal: file cannot be snapshotted, treat the write as irreversible")

type Journal struct {
	dir              string // <state>/journal
	maxSnapshotBytes int64
	mu               sync.Mutex
	log              *os.File // <dir>/<sessionID>.jsonl, O_APPEND
	changes          []Change // replayed from log at Open, appended by Record
}

// Open creates the journal dirs and replays <dir>/<sessionID>.jsonl, dropping a torn tail.
func Open(dir string, sessionID core.ID, maxSnapshotBytes int64) (*Journal, error) {
	if sessionID == "" {
		return nil, errors.New("journal: empty session id")
	}
	if maxSnapshotBytes <= 0 {
		return nil, fmt.Errorf("journal: max snapshot bytes must be positive, got %d", maxSnapshotBytes)
	}
	if err := os.MkdirAll(filepath.Join(dir, "blobs"), journalDirMode); err != nil {
		return nil, fmt.Errorf("journal: create dir: %w", err)
	}
	path := filepath.Join(dir, string(sessionID)+".jsonl")
	changes, err := replayLog(path)
	if err != nil {
		return nil, err
	}
	log, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, journalFileMode)
	if err != nil {
		return nil, fmt.Errorf("journal: open log: %w", err)
	}
	return &Journal{dir: dir, maxSnapshotBytes: maxSnapshotBytes, log: log, changes: changes}, nil
}

func (j *Journal) Close() error {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.log.Close()
}

// Snapshot durably stores path's current bytes and returns their ref; "" means the file is absent.
func (j *Journal) Snapshot(ctx context.Context, path string) (BlobRef, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if !filepath.IsAbs(path) {
		return "", fmt.Errorf("journal: path must be absolute, got %q", path)
	}
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("journal: stat %s: %w", path, err)
	}
	if !info.Mode().IsRegular() || info.Size() > j.maxSnapshotBytes {
		return "", fmt.Errorf("%w: %s", ErrIrreversible, path)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("journal: read %s: %w", path, err)
	}
	if int64(len(data)) > j.maxSnapshotBytes { // grew between Lstat and read
		return "", fmt.Errorf("%w: %s", ErrIrreversible, path)
	}
	sum := sha256.Sum256(data)
	ref := BlobRef(hex.EncodeToString(sum[:]))
	if err := j.writeBlob(ref, data); err != nil {
		return "", err
	}
	return ref, nil
}

// writeBlob stores data at blobs/<ref[:2]>/<ref> via temp+rename; an existing blob is a dedup hit (D-32 rule 2).
func (j *Journal) writeBlob(ref BlobRef, data []byte) error {
	blobs := filepath.Join(j.dir, "blobs")
	shard := filepath.Clean(filepath.Join(blobs, string(ref[:2])))
	blob := filepath.Clean(filepath.Join(shard, string(ref)))
	if _, err := os.Stat(blob); err == nil {
		return nil
	}
	if err := os.MkdirAll(shard, journalDirMode); err != nil {
		return fmt.Errorf("journal: create shard: %w", err)
	}
	tmp, err := os.CreateTemp(shard, ".tmp-*")
	if err != nil {
		return fmt.Errorf("journal: create temp blob: %w", err)
	}
	tmpName := tmp.Name()
	_, err = tmp.Write(data)
	if err == nil {
		err = tmp.Sync()
	}
	if closeErr := tmp.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		_ = os.Remove(tmpName)
		return fmt.Errorf("journal: write temp blob: %w", err)
	}
	if err := os.Rename(tmpName, blob); err != nil {
		_ = os.Remove(tmpName)
		if _, statErr := os.Stat(blob); statErr != nil { // no concurrent session won the race
			return fmt.Errorf("journal: commit blob: %w", err)
		}
	}
	if err := syncDir(shard); err != nil {
		return fmt.Errorf("journal: sync shard: %w", err)
	}
	if err := syncDir(blobs); err != nil {
		return fmt.Errorf("journal: sync blobs: %w", err)
	}
	return nil
}

// replayLog decodes every complete line. A partial or malformed last line is a crash
// mid-append, so it is truncated away; otherwise the next append would glue onto it.
func replayLog(path string) ([]Change, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("journal: read log: %w", err)
	}
	var changes []Change
	validLen := 0
	for rest := data; len(rest) > 0; {
		end := bytes.IndexByte(rest, '\n')
		if end < 0 {
			break
		}
		var change Change
		if err := json.Unmarshal(rest[:end], &change); err != nil {
			if end+1 == len(rest) {
				break
			}
			return nil, fmt.Errorf("journal: corrupt log line at byte %d: %w", validLen, err)
		}
		changes = append(changes, change)
		validLen += end + 1
		rest = rest[end+1:]
	}
	if validLen < len(data) {
		if err := os.Truncate(path, int64(validLen)); err != nil {
			return nil, fmt.Errorf("journal: drop torn log tail: %w", err)
		}
	}
	return changes, nil
}
