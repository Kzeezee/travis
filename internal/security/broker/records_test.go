package broker_test

import (
	"context"
	"errors"
	"os"
	"slices"
	"testing"

	"github.com/Kzeezee/travis/internal/core"
	"github.com/Kzeezee/travis/internal/security/broker"
	"github.com/Kzeezee/travis/internal/state/blobs"
)

// testTurn is the turn writes in these tests are made under; a write outside a turn is refused.
var testTurn = core.WithTurnID(context.Background(), "turn-1")

var errStoreBroken = errors.New("store broken")

// memoryLog is a session log in memory. appendErr refuses appends; onAppend runs before each
// append is accepted, so a test can look at the disk at that exact moment.
type memoryLog struct {
	entries   []core.SessionEntry
	appendErr error
	onAppend  func(entries []core.SessionEntry)
}

func (l *memoryLog) Append(entries ...core.SessionEntry) error {
	if l.onAppend != nil {
		l.onAppend(entries)
	}
	if l.appendErr != nil {
		return l.appendErr
	}
	l.entries = append(l.entries, entries...)
	return nil
}

func (l *memoryLog) ReadEntries() []core.SessionEntry { return slices.Clone(l.entries) }

func (l *memoryLog) reportKinds() []core.SessionEntryKind {
	kinds := make([]core.SessionEntryKind, 0, len(l.entries))
	for _, entry := range l.entries {
		kinds = append(kinds, entry.Kind)
	}
	return kinds
}

// memoryBackups records what each Snapshot saw on disk and keeps the bytes in memory. Files
// larger than maxBytes are irreversible, as the real store reports them.
type memoryBackups struct {
	maxBytes    int
	snapshotErr error
	restoreErr  error
	seen        map[string]string
	blobs       map[blobs.Ref][]byte
}

func (b *memoryBackups) Snapshot(_ context.Context, path string) (blobs.Ref, error) {
	if b.snapshotErr != nil {
		return "", b.snapshotErr
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		b.seen[path] = "<absent>"
		return "", nil
	}
	if err != nil {
		return "", err
	}
	if b.maxBytes > 0 && len(data) > b.maxBytes {
		return "", blobs.ErrIrreversible
	}
	b.seen[path] = string(data)
	ref := blobs.HashContent(data)
	b.blobs[ref] = data
	return ref, nil
}

func (b *memoryBackups) Restore(_ context.Context, path string, ref blobs.Ref) error {
	if b.restoreErr != nil {
		return b.restoreErr
	}
	if ref == "" {
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		return nil
	}
	data, isKept := b.blobs[ref]
	if !isKept {
		return errors.New("no such backup")
	}
	return os.WriteFile(path, data, 0o600)
}

func buildTestRecords() (broker.SessionRecords, *memoryLog, *memoryBackups) {
	log, backups := &memoryLog{}, &memoryBackups{seen: map[string]string{}, blobs: map[blobs.Ref][]byte{}}
	return broker.SessionRecords{Log: log, Backups: backups, IDs: &core.SequentialIDGenerator{Prefix: "entry-"}}, log, backups
}

// discardRecords is for tests about policy alone, which do not look at what was recorded.
func discardRecords() broker.SessionRecords {
	records, _, _ := buildTestRecords()
	return records
}

func findEntries(log *memoryLog, kind core.SessionEntryKind) []core.SessionEntry {
	var found []core.SessionEntry
	for _, entry := range log.entries {
		if entry.Kind == kind {
			found = append(found, entry)
		}
	}
	return found
}

func requireEntryKinds(t *testing.T, log *memoryLog, want ...core.SessionEntryKind) {
	t.Helper()
	if got := log.reportKinds(); !slices.Equal(got, want) {
		t.Fatalf("session log = %v, want %v", got, want)
	}
}
