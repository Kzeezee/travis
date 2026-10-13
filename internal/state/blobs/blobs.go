// Package blobs keeps content-addressed backups of files the broker is about to overwrite, so
// a write can be undone (D-13, DESIGN.md §5.15). Which backup belongs to which write is recorded
// in the session log, not here.
package blobs

// ponytail: no retention, blobs grow until a retention config and GC of unreferenced blobs land

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// Blobs hold file contents and can hold secrets, so only the user may read them.
const (
	blobDirMode  = 0o700
	blobFileMode = 0o600
)

// Ref is the hex SHA-256 of a backup's bytes; the zero value means "the file did not exist".
type Ref string

var ErrIrreversible = errors.New("blobs: file cannot be backed up, treat the write as irreversible")

type Store struct {
	dir              string // <state>/blobs
	maxSnapshotBytes int64
}

func Open(dir string, maxSnapshotBytes int64) (*Store, error) {
	if maxSnapshotBytes <= 0 {
		return nil, fmt.Errorf("blobs: max snapshot bytes must be positive, got %d", maxSnapshotBytes)
	}
	if err := os.MkdirAll(dir, blobDirMode); err != nil {
		return nil, fmt.Errorf("blobs: create dir: %w", err)
	}
	return &Store{dir: dir, maxSnapshotBytes: maxSnapshotBytes}, nil
}

// HashContent is the Ref content would be stored under, without storing it.
func HashContent(content []byte) Ref {
	sum := sha256.Sum256(content)
	return Ref(hex.EncodeToString(sum[:]))
}

// Snapshot durably stores path's current bytes and returns their ref; "" means the file is
// absent. When it returns, the backup and its directory entry are on disk (§5.15.1 step 3).
func (s *Store) Snapshot(ctx context.Context, path string) (Ref, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if !filepath.IsAbs(path) {
		return "", fmt.Errorf("blobs: path must be absolute, got %q", path)
	}
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("blobs: stat %s: %w", path, err)
	}
	if !info.Mode().IsRegular() || info.Size() > s.maxSnapshotBytes {
		return "", fmt.Errorf("%w: %s", ErrIrreversible, path)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("blobs: read %s: %w", path, err)
	}
	if int64(len(data)) > s.maxSnapshotBytes { // grew between Lstat and read
		return "", fmt.Errorf("%w: %s", ErrIrreversible, path)
	}
	ref := HashContent(data)
	if err := s.writeBlob(ref, data); err != nil {
		return "", err
	}
	return ref, nil
}

// writeBlob stores data at <ref[:2]>/<ref> via temp+rename; an existing blob is a dedup hit (D-32 rule 2).
func (s *Store) writeBlob(ref Ref, data []byte) error {
	blob, err := s.buildBlobPath(ref)
	if err != nil {
		return err
	}
	shard := filepath.Dir(blob)
	if _, err := os.Stat(blob); err == nil {
		return nil
	}
	if err := os.MkdirAll(shard, blobDirMode); err != nil {
		return fmt.Errorf("blobs: create shard: %w", err)
	}
	tmp, err := os.CreateTemp(shard, ".tmp-*")
	if err != nil {
		return fmt.Errorf("blobs: create temp blob: %w", err)
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
		return fmt.Errorf("blobs: write temp blob: %w", err)
	}
	if err := os.Rename(tmpName, blob); err != nil {
		_ = os.Remove(tmpName)
		if _, statErr := os.Stat(blob); statErr != nil { // no concurrent session won the race
			return fmt.Errorf("blobs: commit blob: %w", err)
		}
	}
	if err := syncDir(shard); err != nil {
		return fmt.Errorf("blobs: sync shard: %w", err)
	}
	if err := syncDir(s.dir); err != nil {
		return fmt.Errorf("blobs: sync blobs: %w", err)
	}
	return nil
}

// Restore puts path back to the bytes ref names, or removes it when ref is "", the file having
// not existed before. The blob is verified first, so a corrupt backup never reaches the file.
func (s *Store) Restore(ctx context.Context, path string, ref Ref) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if !filepath.IsAbs(path) {
		return fmt.Errorf("blobs: path must be absolute, got %q", path)
	}
	if ref == "" {
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("blobs: remove %s: %w", path, err)
		}
		return nil
	}
	data, err := s.readVerifiedBlob(ref)
	if err != nil {
		return err
	}
	return writeFileDurably(path, data)
}

// readVerifiedBlob rereads a blob and rejects it unless its bytes still hash to ref.
func (s *Store) readVerifiedBlob(ref Ref) ([]byte, error) {
	blob, err := s.buildBlobPath(ref)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(blob)
	if err != nil {
		return nil, fmt.Errorf("blobs: read blob %s: %w", ref, err)
	}
	if HashContent(data) != ref {
		return nil, fmt.Errorf("blobs: blob %s is corrupt", ref)
	}
	return data, nil
}

// buildBlobPath validates ref as a SHA-256 hex digest, since refs read back from a log are untrusted.
func (s *Store) buildBlobPath(ref Ref) (string, error) {
	if digest, err := hex.DecodeString(string(ref)); err != nil || len(digest) != sha256.Size {
		return "", fmt.Errorf("blobs: invalid blob ref %q", ref)
	}
	return filepath.Join(s.dir, string(ref[:2]), string(ref)), nil
}

// writeFileDurably replaces path via temp+rename in its own dir, keeping the current mode.
func writeFileDurably(path string, data []byte) error {
	mode := os.FileMode(blobFileMode) // ponytail: recreated files get 0600, record mode in FileChange if users notice
	if info, err := os.Stat(path); err == nil {
		mode = info.Mode().Perm()
	}
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".travis-restore-*")
	if err != nil {
		return fmt.Errorf("blobs: create temp for %s: %w", path, err)
	}
	tmpName := tmp.Name()
	_, err = tmp.Write(data)
	if err == nil {
		err = tmp.Chmod(mode)
	}
	if err == nil {
		err = tmp.Sync()
	}
	if closeErr := tmp.Close(); err == nil {
		err = closeErr
	}
	if err == nil {
		err = os.Rename(tmpName, path)
	}
	if err != nil {
		_ = os.Remove(tmpName)
		return fmt.Errorf("blobs: restore %s: %w", path, err)
	}
	if err := syncDir(dir); err != nil {
		return fmt.Errorf("blobs: sync %s: %w", dir, err)
	}
	return nil
}
