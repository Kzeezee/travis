package broker

import (
	"context"

	"github.com/Kzeezee/travis/internal/core"
	"github.com/Kzeezee/travis/internal/state/session"
)

// SearchHistory searches this session's own log, in memory: ReadEntries is a mutex-protected
// snapshot (filestore.Store), never a fresh disk read, so this can never race a concurrent
// Append and is unaffected by anything TRA-78's retention sweep does to the file on disk.
func (b *PolicyEnforcedBroker) SearchHistory(_ context.Context, query string, maxResults int) ([]core.HistoryMatch, error) {
	return session.SearchEntries(b.records.Log.ReadEntries(), query, maxResults), nil
}
