package loop

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/Kzeezee/travis/internal/core"
	"github.com/Kzeezee/travis/internal/state/session"
)

// Compaction swaps older turns for a summary in the request only; the session log keeps them verbatim (§5.4.2).
const (
	initialCompactAtFraction = 0.6 // low because the token count is an estimate (D-33)
	minCompactAtFraction     = 0.2
	// afterRejectionFraction sets the new threshold below the size a provider just refused.
	afterRejectionFraction = 0.8
	approxBytesPerToken    = 4
	// ponytail: a tool result is cut to 2000 bytes in the summariser input, so a summary can miss detail past that; raise it if summaries lose what mattered.
	maxSummarisedResultBytes = 2000

	askUserToolName         = "ask_user"
	untrustedContentDropped = "[untrusted content dropped]"
	summariserPrompt        = "Summarise the earlier conversation between a user and their assistant. Be concise. " +
		"Keep the decisions made, the facts learned and the tasks still open. Reply with the summary only."
)

func (c *AssistantConversation) discardSummary() {
	c.summary, c.preserved, c.compactedUntil = "", nil, 0
}

// buildHistory is the conversation the model sees: the summary, then every entry after the summarised span.
func (c *AssistantConversation) buildHistory() []core.Message {
	history := session.BuildHistory(c.entries[c.compactedUntil:])
	if c.summary == "" || len(history) == 0 {
		return history
	}
	note := "[travis] Summary of the earlier conversation:\n" + c.summary + "\n\n"
	if len(c.preserved) > 0 {
		note += "Kept verbatim:\n" + strings.Join(c.preserved, "\n") + "\n\n"
	}
	history[0].Parts = append([]core.Part{core.TextPart{Text: note}}, history[0].Parts...)
	return history
}

func estimateRequestTokens(request core.ProviderRequest) int {
	encoded, err := json.Marshal(request)
	if err != nil {
		return 0
	}
	return len(encoded) / approxBytesPerToken
}

func (c *AssistantConversation) shouldCompact(request core.ProviderRequest) bool {
	return c.contextLength > 0 && float64(estimateRequestTokens(request)) > c.compactAtFraction*float64(c.contextLength)
}

// lowerCompactionThreshold treats a rejected request as proof the estimate ran low (D-33).
func (c *AssistantConversation) lowerCompactionThreshold(rejected core.ProviderRequest) {
	rejectedFraction := float64(estimateRequestTokens(rejected)) / float64(c.contextLength)
	c.compactAtFraction = max(minCompactAtFraction, min(c.compactAtFraction, afterRejectionFraction*rejectedFraction))
}

// compactHistory summarises the oldest finished turns that fit the summariser's input budget and reports
// whether it removed any; a summariser that fails leaves the history as it was.
func (c *AssistantConversation) compactHistory(ctx context.Context) bool {
	// A file-revert note ahead of the current turn stays in the request, so it is not part of the span.
	end := len(c.entries)
	for end > 0 && c.entries[end-1].Turn == c.turnID {
		end--
	}
	for end > 0 && c.entries[end-1].Kind == core.EntryFilesReverted {
		end--
	}
	cut := c.selectCompactionCut(end)
	span := session.BuildHistory(c.entries[c.compactedUntil:cut])
	if len(span) == 0 {
		return false
	}
	summary, isSummarised := c.summariseSpan(ctx, span)
	if !isSummarised {
		return false
	}
	c.summary, c.compactedUntil = summary, cut
	for _, line := range listPreservedLines(span) {
		if !slices.Contains(c.preserved, line) {
			c.preserved = append(c.preserved, line)
		}
	}
	return true
}

// selectCompactionCut is where the summarised span ends: the start of the first turn left out once the oldest
// turns fill the summariser's byte budget, but always after at least the oldest turn.
func (c *AssistantConversation) selectCompactionCut(end int) int {
	budget := int(c.compactAtFraction * float64(c.contextLength) * approxBytesPerToken)
	cuts := append(c.listTurnStarts(end), end)
	selected := cuts[0]
	for _, cut := range cuts[1:] {
		span := session.BuildHistory(c.entries[c.compactedUntil:cut])
		if len(renderSpanForSummary(c.summary, span)) > budget {
			break
		}
		selected = cut
	}
	return selected
}

// listTurnStarts lists where each turn after the first begins in the unsummarised entries; it skips a start
// that a later rewind reaches back across, as that rewind would be left without the turn it hides.
func (c *AssistantConversation) listTurnStarts(end int) []int {
	var starts []int
	var previousTurn core.ID
	hasPreviousTurn := false
	for i := c.compactedUntil; i < end; i++ {
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
	for _, entry := range c.entries[cut:end] {
		if entry.Kind != core.EntryRewound || entry.Rewind == nil {
			continue
		}
		start := slices.IndexFunc(c.entries[c.compactedUntil:end], func(e core.SessionEntry) bool { return e.Turn == entry.Rewind.FromTurn })
		if start >= 0 && c.compactedUntil+start < cut {
			return true
		}
	}
	return false
}

func (c *AssistantConversation) summariseSpan(ctx context.Context, span []core.Message) (string, bool) {
	input := core.Message{Role: core.RoleUser, Parts: []core.Part{core.TextPart{Text: renderSpanForSummary(c.summary, span)}}}
	stream, err := c.provider.Chat(ctx, core.ProviderRequest{Model: c.model, System: summariserPrompt, Messages: []core.Message{input}})
	if err != nil {
		return "", false
	}
	defer func() { _ = stream.Close() }()

	var text strings.Builder
	var usage core.Usage
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
	}
	c.chargeStep(ctx, usage)
	summary := strings.TrimSpace(text.String())
	return summary, summary != ""
}

// renderSpanForSummary is all the summariser sees; an untrusted result is dropped here, one by one (D-26).
func renderSpanForSummary(previousSummary string, span []core.Message) string {
	var text strings.Builder
	if previousSummary != "" {
		text.WriteString("Summary so far:\n" + previousSummary + "\n\n")
	}
	text.WriteString("Conversation to summarise:\n")
	for _, message := range span {
		for _, part := range message.Parts {
			switch part := part.(type) {
			case core.TextPart:
				text.WriteString(string(message.Role) + ": " + part.Text + "\n")
			case core.ToolCallPart:
				text.WriteString("assistant called tool " + part.ToolCall.Name + "\n")
			case core.ToolResultPart:
				content := part.ToolResult.Content
				if part.ToolResult.IsUntrusted {
					content = untrustedContentDropped
				}
				content = truncateAtRuneBoundary(content, maxSummarisedResultBytes)
				text.WriteString("tool " + part.ToolResult.Name + ": " + content + "\n")
			}
		}
	}
	return text.String()
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

// listPreservedLines is what code keeps word for word, because a summariser may blur it: the user's
// answers and the paths in use. The current task is the current turn's own message, never in the span.
// A call made after untrusted content in its turn, or one that failed, is not kept: its arguments may be injected (D-26).
func listPreservedLines(span []core.Message) []string {
	type keptCall struct {
		path, question string
		isAskUser      bool
	}
	var lines []string
	calls := map[core.ID]keptCall{}
	isTurnTainted := false
	for _, message := range span {
		if message.Role == core.RoleUser {
			isTurnTainted = false
		}
		for _, part := range message.Parts {
			switch part := part.(type) {
			case core.ToolCallPart:
				if isTurnTainted {
					continue
				}
				var args struct {
					Path     string `json:"path"`
					Question string `json:"question"`
				}
				_ = json.Unmarshal(part.ToolCall.Arguments, &args)
				calls[part.ToolCall.ID] = keptCall{path: args.Path, question: args.Question, isAskUser: part.ToolCall.Name == askUserToolName}
			case core.ToolResultPart:
				result := part.ToolResult
				call, isKept := calls[result.CallID]
				if result.IsUntrusted {
					isTurnTainted = true
				}
				if !isKept || result.IsUntrusted || result.IsError {
					continue
				}
				if call.path != "" {
					lines = append(lines, "Path in use: "+call.path)
				}
				if call.isAskUser {
					lines = append(lines, "Asked the user: "+call.question+"\nThe user answered: "+result.Content)
				}
			}
		}
	}
	return lines
}
