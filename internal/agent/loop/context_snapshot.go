package loop

import (
	"encoding/json"
	"fmt"

	"github.com/Kzeezee/travis/internal/core"
)

// ContextSnapshot describes the exact latest request bytes and estimated token contribution.
type ContextSnapshot struct {
	Model                   string
	ContextLength           int
	ProviderInputTokens     int
	HasProviderInputTokens  bool
	SummarizerInputTokens   int
	SummarizerOutputTokens  int
	HasSummarizerUsage      bool
	RequestBytes            int
	EstimatedRequestTokens  int
	SystemBytes             int
	MessageBytes            int
	ToolSchemaBytes         int
	MessageCount            int
	ToolCount               int
	FoldedBlockCount        int
	FoldedEntryCount        int
	CompactionThreshold     float64
	PreservedItemCount      int
	UntrustedReplyCount     int
	DroppedUntrustedReplies int
}

func (c *AssistantConversation) captureContextSnapshot(request core.ProviderRequest) {
	requestJSON, _ := json.Marshal(request)
	messagesJSON, _ := json.Marshal(request.Messages)
	toolsJSON, _ := json.Marshal(request.Tools)
	snapshot := ContextSnapshot{
		Model: request.Model, ContextLength: c.contextLength, RequestBytes: len(requestJSON),
		EstimatedRequestTokens: len(requestJSON) / approxBytesPerToken,
		SystemBytes:            len(request.System), MessageBytes: len(messagesJSON), ToolSchemaBytes: len(toolsJSON),
		MessageCount: len(request.Messages), ToolCount: len(request.Tools), FoldedBlockCount: len(c.blocks),
		FoldedEntryCount: c.reportFoldedUntil(), CompactionThreshold: c.compactAtFraction,
	}
	for _, block := range c.blocks {
		snapshot.PreservedItemCount += len(block.preserved)
		snapshot.UntrustedReplyCount += len(block.untrustedReplies)
		snapshot.DroppedUntrustedReplies += block.droppedUntrustedReplies
	}
	c.contextSnapshotMu.Lock()
	snapshot.SummarizerInputTokens = c.pendingSummaryUsage.InputTokens
	snapshot.SummarizerOutputTokens = c.pendingSummaryUsage.OutputTokens
	snapshot.HasSummarizerUsage = c.hasPendingSummaryUsage
	c.pendingSummaryUsage = core.Usage{}
	c.hasPendingSummaryUsage = false
	c.contextSnapshot = snapshot
	c.contextSnapshotMu.Unlock()
}

func (c *AssistantConversation) attachContextUsage(usage core.Usage, hasUsage bool) {
	c.contextSnapshotMu.Lock()
	c.contextSnapshot.ProviderInputTokens = usage.InputTokens
	c.contextSnapshot.HasProviderInputTokens = hasUsage
	c.contextSnapshotMu.Unlock()
}

func (c *AssistantConversation) clearContextSnapshot() {
	c.contextSnapshotMu.Lock()
	c.contextSnapshot = ContextSnapshot{}
	c.pendingSummaryUsage = core.Usage{}
	c.hasPendingSummaryUsage = false
	c.contextSnapshotMu.Unlock()
}

func (c *AssistantConversation) recordSummarizerUsage(usage core.Usage, hasUsage bool) {
	if !hasUsage {
		return
	}
	c.contextSnapshotMu.Lock()
	c.pendingSummaryUsage.InputTokens += usage.InputTokens
	c.pendingSummaryUsage.OutputTokens += usage.OutputTokens
	c.hasPendingSummaryUsage = true
	c.contextSnapshotMu.Unlock()
}

// ReportContextSnapshot returns the latest request snapshot without rebuilding or causing effects.
func (c *AssistantConversation) ReportContextSnapshot() ContextSnapshot {
	c.contextSnapshotMu.RLock()
	defer c.contextSnapshotMu.RUnlock()
	return c.contextSnapshot
}

// SetBudgetLimits changes later-turn cost ceilings while no turn is running.
func (c *AssistantConversation) SetBudgetLimits(maxTurnUSD, maxDailyUSD float64) error {
	if maxTurnUSD < 0 || maxDailyUSD < 0 {
		return fmt.Errorf("cost limits cannot be negative: %w", core.ErrInvalid)
	}
	if !c.busy.TryLock() {
		return fmt.Errorf("cannot change cost limits during a turn: %w", core.ErrInvalid)
	}
	defer c.busy.Unlock()
	c.budget.MaxTurnUSD, c.budget.MaxDailyUSD = maxTurnUSD, maxDailyUSD
	return nil
}
