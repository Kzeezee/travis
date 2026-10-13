package broker

import (
	"context"
	"fmt"
	"os"
	"slices"

	"github.com/Kzeezee/travis/internal/core"
	"github.com/Kzeezee/travis/internal/state/blobs"
	"github.com/Kzeezee/travis/internal/state/session"
)

// Reasons a rewind leaves a file as it is; each is shown to the user and the model verbatim.
const (
	skipChangedSince = "changed since the agent last wrote it"
	skipNoBackup     = "too large to have been backed up"
	skipRestoreError = "could not be restored"
)

// Rewind takes back fromTurn and every later turn, as mode says (§6.4, /undo). Files are put
// back first, then the rewind is logged, so a crash in between leaves files restored but still
// logged as changed: the next rewind then sees they differ from what was written and leaves them
// alone, never overwriting anything. Only this session's own logged changes are ever restored.
func (b *PolicyEnforcedBroker) Rewind(ctx context.Context, fromTurn core.ID, mode core.RewindMode) (core.RewindReport, error) {
	if err := session.ValidateRewindMode(mode); err != nil {
		return core.RewindReport{}, err
	}
	entries := b.records.Log.ReadEntries()
	if fromTurn == "" || !slices.ContainsFunc(entries, func(e core.SessionEntry) bool { return e.Turn == fromTurn }) {
		return core.RewindReport{}, fmt.Errorf("turn %q is not in this session: %w", fromTurn, core.ErrInvalid)
	}

	record := core.RewindRecord{FromTurn: fromTurn, Mode: mode}
	if mode.IsRevertingFiles() {
		plans, err := session.PlanRevert(entries, fromTurn)
		if err != nil {
			return core.RewindReport{}, err
		}
		record.Files, record.ChangeIDs = b.revertFiles(ctx, plans)
		if err := b.appendBrokerFact(ctx, core.SessionEntry{Kind: core.EntryFilesReverted, Rewind: &record}); err != nil {
			return core.RewindReport{}, err
		}
	}
	if mode.IsRewindingConversation() {
		rewound := core.RewindRecord{FromTurn: fromTurn, Mode: mode}
		if err := b.appendBrokerFact(ctx, core.SessionEntry{Kind: core.EntryRewound, Rewind: &rewound}); err != nil {
			return core.RewindReport{}, err
		}
	}
	return core.RewindReport{FromTurn: fromTurn, Mode: mode, Files: b.reportWorkspaceReverts(record.Files)}, nil
}

// revertFiles puts each planned file back, unless it is not what the agent last wrote or there
// is no backup; it reports what it did and the changes it actually reverted.
func (b *PolicyEnforcedBroker) revertFiles(ctx context.Context, plans []session.FileRevertPlan) (core.FileReverts, []core.ID) {
	var reverts core.FileReverts
	var revertedIDs []core.ID
	for _, plan := range plans {
		reason := ""
		resolved, resolveErr := b.resolveWritablePath(ctx, plan.Path)
		switch {
		case plan.HasNoBackup:
			reason = skipNoBackup
		case resolveErr != nil || resolved != plan.Path:
			reason = skipChangedSince
		case reportCurrentContentHash(plan.Path) != plan.ExpectedAfter:
			reason = skipChangedSince
		default:
			if err := b.records.Backups.Restore(ctx, plan.Path, blobs.Ref(plan.Before)); err != nil {
				reason = skipRestoreError + ": " + err.Error()
			}
		}
		switch {
		case reason != "":
			reverts.Skipped = append(reverts.Skipped, core.SkippedFile{Path: plan.Path, Reason: reason})
			continue
		case plan.Before == "":
			reverts.Deleted = append(reverts.Deleted, plan.Path)
		default:
			reverts.Restored = append(reverts.Restored, plan.Path)
		}
		revertedIDs = append(revertedIDs, plan.ChangeIDs...)
	}
	return reverts, revertedIDs
}

// reportCurrentContentHash is the hash a FileChange's After would hold for the file as it is now,
// or "" when it is missing or unreadable, which never matches a write.
func reportCurrentContentHash(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return string(blobs.HashContent(data))
}

func (b *PolicyEnforcedBroker) reportWorkspaceReverts(reverts core.FileReverts) core.FileReverts {
	skipped := make([]core.SkippedFile, 0, len(reverts.Skipped))
	for _, file := range reverts.Skipped {
		skipped = append(skipped, core.SkippedFile{Path: b.describeResolvedPath(file.Path, 0).WorkspacePath, Reason: file.Reason})
	}
	if len(skipped) == 0 {
		skipped = nil
	}
	return core.FileReverts{Restored: b.reportWorkspacePaths(reverts.Restored), Deleted: b.reportWorkspacePaths(reverts.Deleted), Skipped: skipped}
}
