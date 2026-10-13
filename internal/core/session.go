package core

import (
	"context"
	"time"
)

// SessionEntryKind names one kind of fact in the session log (DESIGN.md §5.15). The log is
// append-only: nothing is edited or removed, and state such as the conversation history is
// rebuilt from it.
type SessionEntryKind string

const (
	// EntryMessage is one completed conversation message.
	EntryMessage SessionEntryKind = "message"
	// EntryFileChanged is a file write the broker made, recorded before it was made.
	EntryFileChanged SessionEntryKind = "file_changed"
	// EntryPolicyDenied is an effect the broker refused.
	EntryPolicyDenied SessionEntryKind = "policy_denied"
	// EntryApprovalAsked is an effect the broker put to the human.
	EntryApprovalAsked SessionEntryKind = "approval_asked"
	// EntryApprovalGiven is the human's yes to an approval the broker asked for.
	EntryApprovalGiven SessionEntryKind = "approval_given"
	// EntryFilesReverted is the broker restoring files to how they were before a turn.
	EntryFilesReverted SessionEntryKind = "files_reverted"
	// EntryRewound hides a turn and every later one from the conversation; nothing is deleted.
	EntryRewound SessionEntryKind = "rewound"
)

// RewindMode is what /undo takes back from a turn onward.
type RewindMode string

const (
	RewindFilesAndConversation RewindMode = "both"
	RewindConversationOnly     RewindMode = "conversation"
	RewindFilesOnly            RewindMode = "files"
)

// IsRevertingFiles reports whether the mode puts files back.
func (m RewindMode) IsRevertingFiles() bool {
	return m == RewindFilesAndConversation || m == RewindFilesOnly
}

// IsRewindingConversation reports whether the mode takes turns out of the conversation.
func (m RewindMode) IsRewindingConversation() bool {
	return m == RewindFilesAndConversation || m == RewindConversationOnly
}

// EntryAuthor says which process vouches for an entry. The broker stamps it itself, so travis
// can never claim a fact the broker did not record (§4.4).
type EntryAuthor string

const (
	// AuthorBroker entries are facts the broker caused or decided, and so can vouch for.
	AuthorBroker EntryAuthor = "broker"
	// AuthorTravis entries were reported by travis; the broker vouches only that they were
	// stored as received and never changed since.
	AuthorTravis EntryAuthor = "travis"
)

// SessionEntry is one fact in the session log. Which optional fields are set depends on Kind.
type SessionEntry struct {
	ID     ID               `json:"id"`
	At     time.Time        `json:"at"`
	Author EntryAuthor      `json:"author"`
	Kind   SessionEntryKind `json:"kind"`
	Turn   ID               `json:"turn,omitempty"`
	Call   ID               `json:"call,omitempty"`

	Message    *Message    `json:"message,omitempty"`
	FileChange *FileChange `json:"file_change,omitempty"`
	// Capability is what was denied, asked or approved; Detail is the deciding rule or why it asked.
	Capability *Capability `json:"capability,omitempty"`
	Detail     string      `json:"detail,omitempty"`
	// IsAlways marks an approval_given that became a durable decision rule rather than a once.
	IsAlways bool `json:"always,omitempty"`
	// IsIrreversible marks an approval about a write that cannot be undone.
	IsIrreversible bool `json:"irreversible,omitempty"`
	// Rewind is what a files_reverted or rewound entry did.
	Rewind *RewindRecord `json:"rewind,omitempty"`
}

// RewindRecord is one /undo from FromTurn onward. A files_reverted entry also names the changes
// it reverted, so a later rewind never reverts them twice, and what happened to each file.
type RewindRecord struct {
	FromTurn  ID          `json:"from_turn"`
	Mode      RewindMode  `json:"mode"`
	ChangeIDs []ID        `json:"change_ids,omitempty"`
	Files     FileReverts `json:"files,omitzero"`
}

// FileReverts is what a rewind did to each file it looked at.
type FileReverts struct {
	Restored []string      `json:"restored,omitempty"`
	Deleted  []string      `json:"deleted,omitempty"`
	Skipped  []SkippedFile `json:"skipped,omitempty"`
}

// SkippedFile is a file a rewind left alone, and why; it is never overwritten blind.
type SkippedFile struct {
	Path   string `json:"path"`
	Reason string `json:"reason"`
}

// RewindReport is what /undo did, for the user and the conversation.
type RewindReport struct {
	FromTurn ID
	Mode     RewindMode
	Files    FileReverts
}

// FileChange is what undoing one write needs. Before names the backup of the file's prior
// bytes, empty when the write created the file; After is the hex SHA-256 of the bytes written.
type FileChange struct {
	Path   string `json:"path"`
	Before string `json:"before,omitempty"`
	After  string `json:"after"`
	// IsIrreversible means no backup was taken, so this write cannot be undone.
	IsIrreversible bool `json:"irreversible,omitempty"`
}

// TurnSummary describes one chat turn for a picker: what the user asked and which of the files it
// changed are still changed, so not yet reverted.
type TurnSummary struct {
	Turn            ID
	Prompt          string
	At              time.Time
	ChangedPaths    []string
	HasIrreversible bool
}

// SessionLog is where a conversation records its messages. Append returns only once the
// entries are durable, so a message the caller goes on to act on is never missing from the log.
//
// Rewind takes back fromTurn and every later turn, as mode says: files are restored first, then
// the rewind is logged. It returns what it did, file by file.
type SessionLog interface {
	AppendEntries(ctx context.Context, entries []SessionEntry) error
	ListTurns(ctx context.Context) ([]TurnSummary, error)
	Rewind(ctx context.Context, fromTurn ID, mode RewindMode) (RewindReport, error)
}
