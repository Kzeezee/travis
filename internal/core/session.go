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
)

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

// TurnSummary describes one chat turn for a picker: what the user asked and which files it changed.
type TurnSummary struct {
	Turn            ID
	Prompt          string
	At              time.Time
	ChangedPaths    []string
	HasIrreversible bool
}

// SessionLog is where a conversation records its messages. Append returns only once the
// entries are durable, so a message the caller goes on to act on is never missing from the log.
type SessionLog interface {
	AppendEntries(ctx context.Context, entries []SessionEntry) error
	ListTurns(ctx context.Context) ([]TurnSummary, error)
}
