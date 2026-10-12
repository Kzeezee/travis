package redact

import (
	"context"

	"github.com/Kzeezee/travis/internal/core"
)

// WrapSessionLog returns a SessionLog that redacts every message before next records it, so the
// session log on disk never holds a known secret (§5.12.3).
func WrapSessionLog(next core.SessionLog, redactor Redactor) core.SessionLog {
	return sessionLog{next: next, redactor: redactor}
}

type sessionLog struct {
	next     core.SessionLog
	redactor Redactor
}

func (l sessionLog) AppendEntries(ctx context.Context, entries []core.SessionEntry) error {
	redacted := make([]core.SessionEntry, len(entries))
	for i, entry := range entries {
		if entry.Message != nil {
			message := l.redactor.redactMessage(*entry.Message)
			entry.Message = &message
		}
		redacted[i] = entry
	}
	return l.next.AppendEntries(ctx, redacted)
}

func (l sessionLog) ListTurns(ctx context.Context) ([]core.TurnSummary, error) {
	return l.next.ListTurns(ctx)
}
