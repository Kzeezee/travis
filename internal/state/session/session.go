// Package session holds the rules of the session log (DESIGN.md §5.15): which entries are well
// formed, and how the conversation and the turn list are rebuilt from the log. It does no I/O,
// so the agent loop and the broker apply exactly the same rules.
package session

import (
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	"github.com/Kzeezee/travis/internal/core"
)

// ValidateEntry checks that an entry carries the fields its kind needs and nothing else
// unexpected, since entries are read back from disk and received over the wire.
func ValidateEntry(entry core.SessionEntry) error {
	if entry.Author != core.AuthorBroker && entry.Author != core.AuthorTravis {
		return fmt.Errorf("session entry has unknown author %q: %w", entry.Author, core.ErrInvalid)
	}
	switch entry.Kind {
	case core.EntryMessage:
		return validateMessageEntry(entry)
	case core.EntryFileChanged:
		change := entry.FileChange
		if entry.Turn == "" || change == nil || !filepath.IsAbs(change.Path) || change.After == "" {
			return fmt.Errorf("file_changed needs a turn, an absolute path and an after hash: %w", core.ErrInvalid)
		}
	case core.EntryPolicyDenied, core.EntryApprovalAsked, core.EntryApprovalGiven:
		if entry.Capability == nil || entry.Capability.Kind == "" {
			return fmt.Errorf("%s needs a capability: %w", entry.Kind, core.ErrInvalid)
		}
	default:
		return fmt.Errorf("unknown session entry kind %q: %w", entry.Kind, core.ErrInvalid)
	}
	return nil
}

func validateMessageEntry(entry core.SessionEntry) error {
	if entry.Turn == "" || entry.Message == nil {
		return fmt.Errorf("message entry needs a turn and a message: %w", core.ErrInvalid)
	}
	switch entry.Message.Role {
	case core.RoleUser, core.RoleAssistant, core.RoleTool:
	case core.RoleSystem:
		return fmt.Errorf("the system prompt is rebuilt each step, never logged: %w", core.ErrInvalid)
	default:
		return fmt.Errorf("message entry has unknown role %q: %w", entry.Message.Role, core.ErrInvalid)
	}
	if entry.FileChange != nil || entry.Capability != nil {
		return fmt.Errorf("message entry carries fields of another kind: %w", core.ErrInvalid)
	}
	return nil
}

// ValidateTravisReported checks an entry travis asks the broker to append. Travis may only
// report conversation messages; every other kind is a fact only the broker can vouch for.
func ValidateTravisReported(entry core.SessionEntry) error {
	if entry.Kind != core.EntryMessage {
		return fmt.Errorf("travis cannot report a %s entry, only the broker records those: %w", entry.Kind, core.ErrDenied)
	}
	entry.Author = core.AuthorTravis
	return ValidateEntry(entry)
}

// BuildHistory is the conversation the model sees: every logged message, in log order.
func BuildHistory(entries []core.SessionEntry) []core.Message {
	var history []core.Message
	for _, entry := range entries {
		if entry.Kind == core.EntryMessage && entry.Message != nil {
			history = append(history, *entry.Message)
		}
	}
	return history
}

// BuildTurnSummaries lists the turns in the order they started, each with the user's prompt and
// the files the broker changed during it.
func BuildTurnSummaries(entries []core.SessionEntry) []core.TurnSummary {
	var summaries []core.TurnSummary
	indexByTurn := map[core.ID]int{}
	for _, entry := range entries {
		if entry.Turn == "" {
			continue
		}
		index, isKnown := indexByTurn[entry.Turn]
		if !isKnown {
			index = len(summaries)
			indexByTurn[entry.Turn] = index
			summaries = append(summaries, core.TurnSummary{Turn: entry.Turn, At: entry.At})
		}
		summary := &summaries[index]
		switch {
		case entry.Kind == core.EntryMessage && entry.Message != nil && entry.Message.Role == core.RoleUser && summary.Prompt == "":
			summary.Prompt = joinMessageText(*entry.Message)
		case entry.Kind == core.EntryFileChanged && entry.FileChange != nil:
			if !slices.Contains(summary.ChangedPaths, entry.FileChange.Path) {
				summary.ChangedPaths = append(summary.ChangedPaths, entry.FileChange.Path)
			}
			summary.HasIrreversible = summary.HasIrreversible || entry.FileChange.IsIrreversible
		}
	}
	return summaries
}

func joinMessageText(message core.Message) string {
	var texts []string
	for _, part := range message.Parts {
		if text, isText := part.(core.TextPart); isText {
			texts = append(texts, text.Text)
		}
	}
	return strings.Join(texts, "\n")
}
