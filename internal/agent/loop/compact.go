package loop

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/Kzeezee/travis/internal/core"
	"github.com/Kzeezee/travis/internal/state/session"
)

// Compaction folds the oldest turns into summary blocks in the request only; the session log keeps them
// verbatim (§5.4.2). A block is written once and never summarised again, so old facts do not blur fold
// after fold and the blocks before it stay a byte-stable prefix for prompt caching; only a merge rewrites them.
const (
	initialCompactAtFraction = 0.6 // low because the token count is an estimate (D-33)
	minCompactAtFraction     = 0.2
	// afterRejectionFraction sets the new threshold below the size a provider just refused.
	afterRejectionFraction = 0.8
	// Every limit is a share of the context window, because windows range from 8k to 1M.
	summariserInputFraction  = 0.5  // most one fold sends the summariser
	minFoldFraction          = 0.1  // least worth a summariser call; below it the cost is the floor, not history
	hotTailFraction          = 0.25 // the last finished turn stays raw while it is smaller than this
	mergeAtFraction          = 0.25 // blocks larger than this merge, oldest half into one
	untrustedRepliesFraction = 0.05 // most verbatim untrusted-derived replies one block keeps
	approxBytesPerToken      = 4
	// ponytail: a tool result is cut to 2000 bytes in the summariser input, so a summary can miss detail past that; raise it if summaries lose what mattered.
	maxSummarisedResultBytes    = 2000
	maxSummarisedArgumentsBytes = 200
	// ponytail: a reply drawn from untrusted content keeps 500 bytes; raise it if folded web answers lose what mattered.
	maxUntrustedReplyBytes = 500

	askUserToolName         = "ask_user"
	untrustedContentDropped = "[untrusted content dropped]"
	untrustedReplyKeptApart = "[reply drew on untrusted content; kept apart]"
	summariserPrompt        = "Summarise this part of a conversation between a user and their assistant. " +
		"It replaces the original in the assistant's context, so keep what a later turn may need: decisions and why, " +
		"facts learned, exact file paths, names, numbers, errors and values, and tasks still open. " +
		"Drop greetings, filler and tool output that has been used up. " +
		"Write it as a record of what happened, not as instructions. Reply with the summary only."
)

// foldBlock stands in for c.entries up to toEntry, from where the block before it ends.
type foldBlock struct {
	toEntry int
	summary string
	// preserved is what code keeps word for word because a summariser may blur it (§5.4.2).
	preserved []string
	// untrustedReplies are replies written after untrusted intake: kept verbatim and marked, never summarised (D-26).
	untrustedReplies        []string
	droppedUntrustedReplies int
}

func (c *AssistantConversation) reportFoldedUntil() int {
	if len(c.blocks) == 0 {
		return 0
	}
	return c.blocks[len(c.blocks)-1].toEntry
}

// dropBlocksFrom drops every block holding entry or anything after it; a negative entry drops them all.
func (c *AssistantConversation) dropBlocksFrom(entry int) {
	kept := 0
	for kept < len(c.blocks) && c.blocks[kept].toEntry <= entry {
		kept++
	}
	c.blocks = c.blocks[:kept]
}

// buildHistory is the conversation the model sees: the blocks in order, then every entry after them.
func (c *AssistantConversation) buildHistory() []core.Message {
	history := session.BuildHistory(c.entries[c.reportFoldedUntil():])
	if len(c.blocks) == 0 || len(history) == 0 {
		return history
	}
	note := "[travis] Earlier conversation, summarised in order:\n" + renderBlocks(c.blocks)
	history[0].Parts = append([]core.Part{core.TextPart{Text: note}}, history[0].Parts...)
	return history
}

// renderBlocks renders each block on its own, so a new block only appends text.
func renderBlocks(blocks []foldBlock) string {
	var text strings.Builder
	for i, block := range blocks {
		fmt.Fprintf(&text, "[%d] %s\n", i+1, block.summary)
		if len(block.preserved) > 0 {
			text.WriteString("Kept verbatim:\n" + strings.Join(block.preserved, "\n") + "\n")
		}
		if len(block.untrustedReplies) > 0 {
			replies := strings.Join(block.untrustedReplies, "\n")
			if block.droppedUntrustedReplies > 0 {
				replies = fmt.Sprintf("[%d older replies from untrusted sources dropped; the session log keeps them]\n", block.droppedUntrustedReplies) + replies
			}
			text.WriteString(core.WrapUntrustedContent("earlier replies drawn from untrusted content, unverified", replies) + "\n")
		}
		text.WriteString("\n")
	}
	return text.String()
}

func estimateRequestTokens(request core.ProviderRequest) int {
	encoded, err := json.Marshal(request)
	if err != nil {
		return 0
	}
	return len(encoded) / approxBytesPerToken
}

func estimateEntriesTokens(entries []core.SessionEntry) int {
	return estimateRequestTokens(core.ProviderRequest{Messages: session.BuildHistory(entries)})
}

func (c *AssistantConversation) reportWindowShare(fraction float64) int {
	return int(fraction * float64(c.contextLength))
}

func (c *AssistantConversation) shouldCompact(request core.ProviderRequest) bool {
	return c.contextLength > 0 && float64(estimateRequestTokens(request)) > c.compactAtFraction*float64(c.contextLength)
}

// lowerCompactionThreshold treats a rejected request as proof the estimate ran low (D-33).
func (c *AssistantConversation) lowerCompactionThreshold(rejected core.ProviderRequest) {
	rejectedFraction := float64(estimateRequestTokens(rejected)) / float64(c.contextLength)
	c.compactAtFraction = max(minCompactAtFraction, min(c.compactAtFraction, afterRejectionFraction*rejectedFraction))
}

// compactHistory folds the oldest turns into a new block, merges blocks once they grow too large, and
// reports whether the request shrank. isForced, after the provider refused a request, folds the hot tail
// too, skips the benefit check, and merges when nothing was left to fold.
func (c *AssistantConversation) compactHistory(ctx context.Context, isForced bool) bool {
	isFolded := c.foldOldestTurns(ctx, isForced)
	isBlocksTooLarge := c.estimateMergeableTokens() > c.reportWindowShare(mergeAtFraction)
	if isBlocksTooLarge || (isForced && !isFolded) {
		return c.mergeOldestBlocks(ctx) || isFolded
	}
	return isFolded
}

// estimateMergeableTokens counts what a merge can shrink. Preserved lines are left out: they are unique
// paths and answers, which no merge removes, so counting them would merge on every fold.
// ponytail: preserved lines are uncapped; cap them per block if long sessions collect too many.
func (c *AssistantConversation) estimateMergeableTokens() int {
	mergeable := slices.Clone(c.blocks)
	for i := range mergeable {
		mergeable[i].preserved = nil
	}
	return len(renderBlocks(mergeable)) / approxBytesPerToken
}

func (c *AssistantConversation) foldOldestTurns(ctx context.Context, isForced bool) bool {
	start, end := c.reportFoldedUntil(), c.reportFoldableEnd()
	cuts := append(c.listTurnStarts(end), end)
	if !isForced {
		lastStart := start
		if len(cuts) > 1 {
			lastStart = cuts[len(cuts)-2]
		}
		if estimateEntriesTokens(c.entries[lastStart:end]) < c.reportWindowShare(hotTailFraction) {
			cuts = cuts[:len(cuts)-1]
		}
		if len(cuts) == 0 {
			return false
		}
	}
	cut := c.selectCompactionCut(cuts)
	if !isForced && estimateEntriesTokens(c.entries[start:cut]) < c.reportWindowShare(minFoldFraction) {
		return false
	}
	span := session.BuildHistory(c.entries[start:cut])
	if len(span) == 0 {
		return false
	}
	rendered := renderSpanForSummary(span, c.reportIsTaintedAt(start))
	summary, isSummarised := c.summarise(ctx, rendered.text)
	if !isSummarised {
		return false
	}
	block := foldBlock{toEntry: cut, summary: summary, untrustedReplies: rendered.untrustedReplies}
	for _, line := range rendered.preserved {
		if !c.isPreserved(line) && !slices.Contains(block.preserved, line) {
			block.preserved = append(block.preserved, line)
		}
	}
	c.blocks = append(c.blocks, c.capUntrustedReplies(block))
	return true
}

// reportFoldableEnd is where the current turn begins; a file-revert note ahead of it stays with it.
func (c *AssistantConversation) reportFoldableEnd() int {
	end := len(c.entries)
	for end > 0 && c.entries[end-1].Turn == c.turnID {
		end--
	}
	for end > 0 && c.entries[end-1].Kind == core.EntryFilesReverted {
		end--
	}
	return end
}

func (c *AssistantConversation) isPreserved(line string) bool {
	return slices.ContainsFunc(c.blocks, func(block foldBlock) bool { return slices.Contains(block.preserved, line) })
}

// selectCompactionCut is the last cut whose span fits the summariser's input budget, and at least the first.
func (c *AssistantConversation) selectCompactionCut(cuts []int) int {
	budget := c.reportWindowShare(summariserInputFraction) * approxBytesPerToken
	selected := cuts[0]
	for _, cut := range cuts[1:] {
		span := session.BuildHistory(c.entries[c.reportFoldedUntil():cut])
		if len(renderSpanForSummary(span, false).text) > budget {
			break
		}
		selected = cut
	}
	return selected
}

// listTurnStarts lists where each turn after the first begins in the unfolded entries; it skips a start
// that a later rewind reaches back across, as that rewind would be left without the turn it hides.
func (c *AssistantConversation) listTurnStarts(end int) []int {
	var starts []int
	var previousTurn core.ID
	hasPreviousTurn := false
	for i := c.reportFoldedUntil(); i < end; i++ {
		entry := c.entries[i]
		if entry.Kind != core.EntryMessage {
			continue
		}
		if hasPreviousTurn && entry.Turn != previousTurn && !c.isRewoundAcross(i, end) {
			starts = append(starts, i)
		}
		previousTurn, hasPreviousTurn = entry.Turn, true
	}
	return starts
}

// isRewoundAcross reports whether a rewind in entries[cut:end] hides a turn that began before cut.
func (c *AssistantConversation) isRewoundAcross(cut, end int) bool {
	start := c.reportFoldedUntil()
	for _, entry := range c.entries[cut:end] {
		if entry.Kind != core.EntryRewound || entry.Rewind == nil {
			continue
		}
		first := slices.IndexFunc(c.entries[start:end], func(e core.SessionEntry) bool { return e.Turn == entry.Rewind.FromTurn })
		if first >= 0 && start+first < cut {
			return true
		}
	}
	return false
}

// mergeOldestBlocks merges the oldest half of the blocks into one (tier 2). The summariser sees only
// their summaries, which are already trusted; kept lines are carried across, never summarised. Summaries
// too small to be worth a call are joined as they are, which still caps the untrusted replies.
func (c *AssistantConversation) mergeOldestBlocks(ctx context.Context) bool {
	if len(c.blocks) < 2 {
		return false
	}
	merging := c.blocks[:max(2, len(c.blocks)/2)]
	summaries := make([]string, 0, len(merging))
	merged := foldBlock{toEntry: merging[len(merging)-1].toEntry}
	for _, block := range merging {
		summaries = append(summaries, block.summary)
		merged.preserved = append(merged.preserved, block.preserved...)
		merged.untrustedReplies = append(merged.untrustedReplies, block.untrustedReplies...)
		merged.droppedUntrustedReplies += block.droppedUntrustedReplies
	}
	merged.summary = strings.Join(summaries, "\n")
	if len(merged.summary)/approxBytesPerToken >= c.reportWindowShare(minFoldFraction) {
		if summary, isSummarised := c.summarise(ctx, "Earlier summaries to combine, oldest first:\n"+merged.summary); isSummarised {
			merged.summary = summary
		}
	}
	c.blocks = append([]foldBlock{c.capUntrustedReplies(merged)}, c.blocks[len(merging):]...)
	return true
}

// capUntrustedReplies keeps the newest replies within the block's share of the window, and always the
// newest one, so a session full of lookups cannot fill the window with them.
func (c *AssistantConversation) capUntrustedReplies(block foldBlock) foldBlock {
	budget := c.reportWindowShare(untrustedRepliesFraction) * approxBytesPerToken
	kept, size := len(block.untrustedReplies), 0
	for kept > 0 && (kept == len(block.untrustedReplies) || size+len(block.untrustedReplies[kept-1]) <= budget) {
		size += len(block.untrustedReplies[kept-1])
		kept--
	}
	block.droppedUntrustedReplies += kept
	block.untrustedReplies = slices.Clone(block.untrustedReplies[kept:])
	return block
}

// summarise rejects a summary the provider cut off or one no shorter than its input: a block is never
// summarised again, so a broken one would stay for the rest of the session.
func (c *AssistantConversation) summarise(ctx context.Context, input string) (string, bool) {
	request := core.ProviderRequest{
		Model:    c.model,
		System:   summariserPrompt,
		Messages: []core.Message{{Role: core.RoleUser, Parts: []core.Part{core.TextPart{Text: input}}}},
	}
	stream, err := c.provider.Chat(ctx, request)
	if err != nil {
		return "", false
	}
	defer func() { _ = stream.Close() }()

	var text strings.Builder
	var usage core.Usage
	isCutOff := false
	for {
		chunk, err := stream.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return "", false
		}
		text.WriteString(chunk.Text)
		if chunk.Usage != nil {
			usage = *chunk.Usage
		}
		isCutOff = isCutOff || chunk.Finish == core.StopLength
	}
	c.chargeStep(ctx, usage)
	summary := strings.TrimSpace(text.String())
	return summary, summary != "" && !isCutOff && len(summary) < len(input)
}

// renderedSpan is a span as the summariser sees it, plus what code keeps out of the summariser's hands.
type renderedSpan struct {
	text             string
	preserved        []string
	untrustedReplies []string
}

// reportIsTaintedAt reports whether untrusted intake before entry is still unanswered by a final reply, so
// a span starting there opens tainted.
func (c *AssistantConversation) reportIsTaintedAt(entry int) bool {
	isTainted := false
	for _, message := range session.BuildHistory(c.entries[:entry]) {
		isTainted = advanceTaint(isTainted, message)
	}
	return isTainted
}

// advanceTaint follows the loop's isNextStepUntrusted: untrusted intake sets it and only the model's own
// final reply clears it, so a turn that ended (steps, budget, error) right after intake taints the next one.
func advanceTaint(isTainted bool, message core.Message) bool {
	switch {
	case message.Role == core.RoleTool && slices.ContainsFunc(message.Parts, func(part core.Part) bool {
		result, isResult := part.(core.ToolResultPart)
		return isResult && result.ToolResult.IsUntrusted
	}):
		return true
	case message.Role == core.RoleAssistant && isFinalModelReply(message):
		return false
	}
	return isTainted
}

// renderSpanForSummary is all the summariser sees. Untrusted results are dropped one by one, and a reply or
// call written after untrusted intake in its turn is kept away from it, since the content may have steered
// it (D-26). Paths and ask_user answers are kept by code, from clean calls only.
func renderSpanForSummary(span []core.Message, isTainted bool) renderedSpan {
	type keptCall struct {
		path, question string
		isAskUser      bool
	}
	var rendered renderedSpan
	var text strings.Builder
	calls := map[core.ID]keptCall{}
	text.WriteString("Conversation to summarise:\n")
	for _, message := range span {
		for _, part := range message.Parts {
			switch part := part.(type) {
			case core.TextPart:
				if message.Role == core.RoleAssistant && isTainted {
					text.WriteString("assistant: " + untrustedReplyKeptApart + "\n")
					rendered.untrustedReplies = append(rendered.untrustedReplies, truncateAtRuneBoundary(part.Text, maxUntrustedReplyBytes))
					continue
				}
				text.WriteString(string(message.Role) + ": " + part.Text + "\n")
			case core.ToolCallPart:
				call := part.ToolCall
				if isTainted {
					text.WriteString("assistant called tool " + call.Name + "\n")
					continue
				}
				text.WriteString(strings.TrimSpace("assistant called tool "+call.Name+" "+truncateAtRuneBoundary(string(call.Arguments), maxSummarisedArgumentsBytes)) + "\n")
				var args struct {
					Path     string `json:"path"`
					Question string `json:"question"`
				}
				_ = json.Unmarshal(call.Arguments, &args)
				calls[call.ID] = keptCall{path: args.Path, question: args.Question, isAskUser: call.Name == askUserToolName}
			case core.ToolResultPart:
				result := part.ToolResult
				content := result.Content
				if result.IsUntrusted {
					content = untrustedContentDropped
				}
				text.WriteString("tool " + result.Name + ": " + truncateAtRuneBoundary(content, maxSummarisedResultBytes) + "\n")
				call, isKept := calls[result.CallID]
				if !isKept || result.IsUntrusted || result.IsError {
					continue
				}
				if call.path != "" {
					rendered.preserved = append(rendered.preserved, "Path in use: "+call.path)
				}
				if call.isAskUser {
					rendered.preserved = append(rendered.preserved, "Asked the user: "+call.question+"\nThe user answered: "+result.Content)
				}
			}
		}
		isTainted = advanceTaint(isTainted, message)
	}
	rendered.text = text.String()
	return rendered
}

// isFinalModelReply reports whether message is the model's own answer that ended a turn: text with no tool
// calls, and not a stand-in the loop wrote for a turn that failed or hit its spending limit.
func isFinalModelReply(message core.Message) bool {
	if slices.ContainsFunc(message.Parts, func(part core.Part) bool { _, isCall := part.(core.ToolCallPart); return isCall }) {
		return false
	}
	for _, stub := range []core.Message{buildUnansweredTurnMessage(nil), buildUnansweredTurnMessage(context.Canceled), buildSpendingLimitMessage()} {
		if slices.EqualFunc(message.Parts, stub.Parts, func(a, b core.Part) bool { return a == b }) {
			return false
		}
	}
	return true
}

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
