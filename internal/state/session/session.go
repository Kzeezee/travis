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
	case core.EntryFilesReverted, core.EntryRewound:
		return validateRewindEntry(entry)
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

func validateRewindEntry(entry core.SessionEntry) error {
	record := entry.Rewind
	if record == nil || record.FromTurn == "" {
		return fmt.Errorf("%s needs the turn it rewinds from: %w", entry.Kind, core.ErrInvalid)
	}
	if err := ValidateRewindMode(record.Mode); err != nil {
		return err
	}
	isFileMode, isConversationMode := record.Mode.IsRevertingFiles(), record.Mode.IsRewindingConversation()
	if (entry.Kind == core.EntryFilesReverted && !isFileMode) || (entry.Kind == core.EntryRewound && !isConversationMode) {
		return fmt.Errorf("%s does not belong to a %s rewind: %w", entry.Kind, record.Mode, core.ErrInvalid)
	}
	return nil
}

// ValidateRewindMode refuses any mode but the three /undo offers.
func ValidateRewindMode(mode core.RewindMode) error {
	switch mode {
	case core.RewindFilesAndConversation, core.RewindConversationOnly, core.RewindFilesOnly:
		return nil
	}
	return fmt.Errorf("unknown rewind mode %q: %w", mode, core.ErrInvalid)
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

// BuildHistory is the conversation the model sees: the logged messages a rewind has not taken
// out, in log order. A files-only rewind left the conversation as it was, so the model is told
// which files changed back, at the start of the next user message.
func BuildHistory(entries []core.SessionEntry) []core.Message {
	var history []core.Message
	var pendingNotes []core.Part
	for _, entry := range selectVisibleEntries(entries) {
		switch {
		case entry.Kind == core.EntryFilesReverted && entry.Rewind.Mode == core.RewindFilesOnly:
			pendingNotes = append(pendingNotes, core.TextPart{Text: describeFileRevert(*entry.Rewind)})
		case entry.Kind == core.EntryMessage && entry.Message != nil:
			message := *entry.Message
			if message.Role == core.RoleUser && len(pendingNotes) > 0 {
				message.Parts = append(pendingNotes, message.Parts...)
				pendingNotes = nil
			}
			history = append(history, message)
		}
	}
	return history
}

// selectVisibleEntries drops what rewinds took out: a rewound entry hides its turn and every
// entry after that turn began, up to the rewind itself. Entries logged later count again.
func selectVisibleEntries(entries []core.SessionEntry) []core.SessionEntry {
	var visible []core.SessionEntry
	for _, entry := range entries {
		if entry.Kind == core.EntryRewound && entry.Rewind != nil {
			if start := slices.IndexFunc(visible, func(e core.SessionEntry) bool { return e.Turn == entry.Rewind.FromTurn }); start >= 0 {
				visible = visible[:start]
			}
			continue
		}
		visible = append(visible, entry)
	}
	return visible
}

func describeFileRevert(record core.RewindRecord) string {
	var text strings.Builder
	text.WriteString("[travis] The user reverted files to how they were before an earlier turn; the conversation was kept.")
	writePathList(&text, "Restored", record.Files.Restored)
	writePathList(&text, "Deleted, as the turn had created them", record.Files.Deleted)
	skipped := make([]string, 0, len(record.Files.Skipped))
	for _, file := range record.Files.Skipped {
		skipped = append(skipped, file.Path+" ("+file.Reason+")")
	}
	writePathList(&text, "Left as they are", skipped)
	return text.String()
}

func writePathList(text *strings.Builder, label string, paths []string) {
	if len(paths) > 0 {
		text.WriteString(" " + label + ": " + strings.Join(paths, ", ") + ".")
	}
}

// BuildTurnSummaries lists the turns still in the conversation, in the order they started, each
// with the user's prompt and the files the broker changed during it that are not yet reverted.
func BuildTurnSummaries(entries []core.SessionEntry) []core.TurnSummary {
	reverted := collectRevertedChangeIDs(entries)
	var summaries []core.TurnSummary
	indexByTurn := map[core.ID]int{}
	for _, entry := range selectVisibleEntries(entries) {
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
		case entry.Kind == core.EntryFileChanged && entry.FileChange != nil && !reverted[entry.ID]:
			if !slices.Contains(summary.ChangedPaths, entry.FileChange.Path) {
				summary.ChangedPaths = append(summary.ChangedPaths, entry.FileChange.Path)
			}
			summary.HasIrreversible = summary.HasIrreversible || entry.FileChange.IsIrreversible
		}
	}
	return summaries
}

func collectRevertedChangeIDs(entries []core.SessionEntry) map[core.ID]bool {
	reverted := map[core.ID]bool{}
	for _, entry := range entries {
		if entry.Kind == core.EntryFilesReverted && entry.Rewind != nil {
			for _, id := range entry.Rewind.ChangeIDs {
				reverted[id] = true
			}
		}
	}
	return reverted
}

// FileRevertPlan is how to put one file back. Before is the earliest change's backup, the state
// before fromTurn; ExpectedAfter is what the latest change wrote, so a file edited since by
// someone else can be told apart and left alone.
type FileRevertPlan struct {
	Path          string
	Before        string
	ExpectedAfter string
	// HasNoBackup means the earliest change could not be backed up, so there is nothing to restore.
	HasNoBackup bool
	ChangeIDs   []core.ID
}

// PlanRevert works out, per file, how to undo every change made from fromTurn onward that no
// earlier rewind already reverted. Log order decides, so it also covers turns a conversation-only
// rewind took out of the chat but whose files were never put back.
func PlanRevert(entries []core.SessionEntry, fromTurn core.ID) ([]FileRevertPlan, error) {
	start := slices.IndexFunc(entries, func(e core.SessionEntry) bool { return e.Turn == fromTurn })
	if fromTurn == "" || start < 0 {
		return nil, fmt.Errorf("turn %q is not in this session: %w", fromTurn, core.ErrInvalid)
	}
	reverted := collectRevertedChangeIDs(entries)
	var plans []FileRevertPlan
	indexByPath := map[string]int{}
	for _, entry := range entries[start:] {
		if entry.Kind != core.EntryFileChanged || entry.FileChange == nil || reverted[entry.ID] {
			continue
		}
		change := entry.FileChange
		index, isKnown := indexByPath[change.Path]
		if !isKnown {
			index = len(plans)
			indexByPath[change.Path] = index
			plans = append(plans, FileRevertPlan{Path: change.Path, Before: change.Before, HasNoBackup: change.IsIrreversible})
		}
		plans[index].ExpectedAfter = change.After
		plans[index].ChangeIDs = append(plans[index].ChangeIDs, entry.ID)
	}
	return plans, nil
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
