package core

import "context"

// MemoryStore owns the one long-term note file; callers never choose its path.
type MemoryStore interface {
	ReadMemory(ctx context.Context) (string, error)
	ReplaceMemory(ctx context.Context, content string) error
}

type MemoryChangeKind string

const (
	MemoryAppendTrusted  MemoryChangeKind = "append_trusted"
	MemoryReplaceTrusted MemoryChangeKind = "replace_trusted"
	MemoryClearTrusted   MemoryChangeKind = "clear_trusted"
)

// MemoryDocument is the broker-owned split view used by human memory editing.
type MemoryDocument struct {
	Trusted, Untrusted string
	Revision           string
	Bytes, Lines       int
	MaxBytes, MaxLines int
}

type MemoryChange struct {
	Kind             MemoryChangeKind
	Content          string
	ExpectedRevision string
}

// MemoryEditor applies conflict-aware human edits without exposing file-format delimiters.
type MemoryEditor interface {
	ReadMemoryDocument(ctx context.Context) (MemoryDocument, error)
	ChangeMemory(ctx context.Context, change MemoryChange) (MemoryDocument, error)
}
