package broker

import (
	"context"
	"errors"
	"fmt"

	"github.com/Kzeezee/travis/internal/core"
	"github.com/Kzeezee/travis/internal/state/blobs"
	"github.com/Kzeezee/travis/internal/state/session"
)

// SessionRecorder is the broker's append-only session log. Append returns once the entries are
// durable; ReadEntries returns everything appended so far, in order.
type SessionRecorder interface {
	Append(entries ...core.SessionEntry) error
	ReadEntries() []core.SessionEntry
}

// FileBackups stores a file's current bytes before the broker overwrites it. Snapshot returns
// "" for a file that does not exist yet, and an error wrapping blobs.ErrIrreversible for one it
// cannot back up. Restore puts a backup back, removing the file for "".
type FileBackups interface {
	Snapshot(ctx context.Context, path string) (blobs.Ref, error)
	Restore(ctx context.Context, path string, ref blobs.Ref) error
}

// SessionRecords are where a PolicyEnforcedBroker keeps its record of the session; IDs names
// each entry. All three are required: the broker never acts without recording it.
type SessionRecords struct {
	Log     SessionRecorder
	Backups FileBackups
	IDs     core.IDGenerator
}

// backUpAndLogWrite is §5.15.1's order: the target's bytes are backed up and synced, then the
// change is logged and synced, and only then does the caller write. A write outside a turn is
// refused, since nothing could later select it to undo.
func (b *PolicyEnforcedBroker) backUpAndLogWrite(ctx context.Context, target string, content []byte) error {
	if core.ReportTurnID(ctx) == "" {
		return fmt.Errorf("%q: a write outside an assistant turn could never be undone: %w", target, core.ErrDenied)
	}
	before, err := b.records.Backups.Snapshot(ctx, target)
	isIrreversible := errors.Is(err, blobs.ErrIrreversible)
	if err != nil && !isIrreversible {
		return fmt.Errorf("%q: back up before writing: %w", target, err)
	}
	if isIrreversible {
		if err := b.requireIrreversibleApproval(ctx, target); err != nil {
			return err
		}
	}
	change := &core.FileChange{Path: target, Before: string(before), After: string(blobs.HashContent(content)), IsIrreversible: isIrreversible}
	return b.appendBrokerFact(ctx, core.SessionEntry{Kind: core.EntryFileChanged, FileChange: change})
}

// requireIrreversibleApproval lets a write the broker cannot back up through only on a fresh
// once-approval for exactly that target (§6.4: irreversible means ask, always).
func (b *PolicyEnforcedBroker) requireIrreversibleApproval(ctx context.Context, target string) error {
	write := core.Capability{Kind: core.CapabilityFSWrite, Scope: target}
	if b.approvals.irreversibleAllowed[write] {
		return nil
	}
	const explanation = "the file is too large to back up, so this write cannot be undone"
	required := &core.ApprovalRequiredError{Operation: core.Operation{Kind: write.Kind, Target: target, Explanation: explanation, IsIrreversible: true}}
	if err := b.appendBrokerFact(ctx, core.SessionEntry{Kind: core.EntryApprovalAsked, Capability: &write, Detail: explanation, IsIrreversible: true}); err != nil {
		return err
	}
	b.approvals.irreversibleAsked[write] = true
	return required
}

// appendBrokerFact stamps an entry as the broker's own, under the turn and tool call ctx serves.
func (b *PolicyEnforcedBroker) appendBrokerFact(ctx context.Context, entry core.SessionEntry) error {
	entry.ID, entry.At, entry.Author = b.records.IDs.GenerateID(), b.clock.ReportCurrentTime().UTC(), core.AuthorBroker
	entry.Turn, entry.Call = core.ReportTurnID(ctx), core.ReportToolCallID(ctx)
	if err := b.records.Log.Append(entry); err != nil {
		return fmt.Errorf("record %s in the session log: %w", entry.Kind, err)
	}
	return nil
}

// AppendTravisEntries logs messages travis reports. The broker stamps them as travis's: it can
// vouch that they are stored as received, never that they are true (§4.4).
func (b *PolicyEnforcedBroker) AppendTravisEntries(_ context.Context, entries []core.SessionEntry) error {
	stamped := make([]core.SessionEntry, 0, len(entries))
	for _, entry := range entries {
		if err := session.ValidateTravisReported(entry); err != nil {
			return err
		}
		entry.ID, entry.At, entry.Author = b.records.IDs.GenerateID(), b.clock.ReportCurrentTime().UTC(), core.AuthorTravis
		stamped = append(stamped, entry)
	}
	return b.records.Log.Append(stamped...)
}

// ListTurns reports the turns still in the conversation, with workspace-relative paths for display.
func (b *PolicyEnforcedBroker) ListTurns(context.Context) ([]core.TurnSummary, error) {
	summaries := session.BuildTurnSummaries(b.records.Log.ReadEntries())
	for i := range summaries {
		summaries[i].ChangedPaths = b.reportWorkspacePaths(summaries[i].ChangedPaths)
	}
	return summaries, nil
}

func (b *PolicyEnforcedBroker) reportWorkspacePaths(paths []string) []string {
	if paths == nil {
		return nil
	}
	reported := make([]string, 0, len(paths))
	for _, path := range paths {
		reported = append(reported, b.describeResolvedPath(path, 0).WorkspacePath)
	}
	return reported
}
