package session

import (
	"strings"
	"unicode/utf8"

	"github.com/Kzeezee/travis/internal/core"
)

// MaxHistoryMatches and MaxHistoryMatchBytes bound what SearchEntries ever returns, regardless
// of what a caller asks for: a manipulated model asking for an inflated maxResults must not be
// able to flood its own context window with cold-tier content (a context-stuffing variant of
// prompt injection, not just a resource concern).
const (
	MaxHistoryMatches    = 20
	MaxHistoryMatchBytes = 4 << 10
)

// SearchEntries runs a plain case-insensitive substring search over entries, never a regular
// expression: a regex taken from model input would let a crafted query cause catastrophic
// backtracking, and DESIGN.md §5.4.2 only asks for grep-equivalent recall, not pattern power.
// Only what the live conversation would show is searched (SelectVisibleEntries): a /undo'd turn
// stays invisible to search the same way it is invisible to the model otherwise. Results are
// newest first and capped at MaxHistoryMatches regardless of maxResults, which can only ask
// for fewer.
func SearchEntries(entries []core.SessionEntry, query string, maxResults int) []core.HistoryMatch {
	limit := MaxHistoryMatches
	if maxResults > 0 && maxResults < limit {
		limit = maxResults
	}
	if query == "" || limit <= 0 {
		return nil
	}
	needle := strings.ToLower(query)

	visible := SelectVisibleEntries(entries)
	var matches []core.HistoryMatch
	for i := len(visible) - 1; i >= 0 && len(matches) < limit; i-- {
		entry := visible[i]
		text, isUntrusted, ok := reportSearchableText(entry)
		if !ok || !strings.Contains(strings.ToLower(text), needle) {
			continue
		}
		matches = append(matches, core.HistoryMatch{
			Turn: entry.Turn, At: entry.At, Kind: entry.Kind,
			Text: truncateAtRuneBoundary(text, MaxHistoryMatchBytes), IsUntrusted: isUntrusted,
		})
	}
	return matches
}

// reportSearchableText is what one entry offers history.search, and whether it carries
// anything untrusted; ok is false for entry kinds search does not cover (approvals, rewinds).
func reportSearchableText(entry core.SessionEntry) (text string, isUntrusted bool, ok bool) {
	switch entry.Kind {
	case core.EntryMessage:
		if entry.Message == nil {
			return "", false, false
		}
		return reportMessageSearchText(*entry.Message)
	case core.EntryFileChanged:
		if entry.FileChange == nil {
			return "", false, false
		}
		return entry.FileChange.Path, false, true
	case core.EntryPolicyDenied, core.EntryApprovalAsked, core.EntryApprovalGiven, core.EntryFilesReverted, core.EntryRewound:
		// Out of scope for v1 (the ticket's own "done when" names recovering an earlier message).
		return "", false, false
	}
	return "", false, false
}

// reportMessageSearchText joins a message's own text and any tool results it carries. A tool
// result's Content already carries its own WrapUntrustedContent delimiters from when it was
// first produced, so nothing here re-wraps it; IsUntrusted is true if any part was.
func reportMessageSearchText(message core.Message) (text string, isUntrusted bool, ok bool) {
	var parts []string
	for _, part := range message.Parts {
		switch p := part.(type) {
		case core.TextPart:
			parts = append(parts, p.Text)
		case core.ToolResultPart:
			parts = append(parts, p.ToolResult.Content)
			isUntrusted = isUntrusted || p.ToolResult.IsUntrusted
		}
	}
	if len(parts) == 0 {
		return "", false, false
	}
	return strings.Join(parts, "\n"), isUntrusted, true
}

// truncateAtRuneBoundary caps text at maxBytes without splitting a multi-byte rune.
func truncateAtRuneBoundary(text string, maxBytes int) string {
	if len(text) <= maxBytes {
		return text
	}
	cut := maxBytes
	for cut > 0 && !utf8.RuneStart(text[cut]) {
		cut--
	}
	return text[:cut] + "…"
}
