package core

import "context"

// MemoryStore owns the one long-term note file; callers never choose its path.
type MemoryStore interface {
	ReadMemory(ctx context.Context) (string, error)
	ReplaceMemory(ctx context.Context, content string) error
}
