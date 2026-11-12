package core

import (
	"context"
	"time"
)

// HistorySearcher searches this session's own log for the "cold tier" DESIGN.md §5.4.2
// describes: facts compaction has folded out of the live request but which still exist
// verbatim on disk. It never reaches another session's log (§5.4.2, out of scope).
type HistorySearcher interface {
	SearchHistory(ctx context.Context, query string, maxResults int) ([]HistoryMatch, error)
}

// HistoryMatch is one entry history.search found. IsUntrusted carries forward whatever the
// entry was marked when it was first logged (a ToolResultPart's own flag); a caller that
// finds any match with IsUntrusted true must treat the whole result as untrusted (D-26 rule
// 2): the cold tier keeps what compaction already dropped specifically to keep it out of
// trusted context, so retrieving it must re-arm the same escalation, not launder it back in.
type HistoryMatch struct {
	Turn        ID
	At          time.Time
	Kind        SessionEntryKind
	Text        string
	IsUntrusted bool
}
