// Package loop owns one assistant turn: it asks the provider, dispatches the tools the
// model calls, relays what happened as observable facts, and keeps the conversation so
// the next prompt has context.
package loop

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"

	"github.com/Kzeezee/travis/internal/core"
	"github.com/Kzeezee/travis/internal/state/session"
)

// ToolLookup is the loop's view of the registry: what to offer the model this step,
// and how to find what it asked for. A nil ToolLookup means no tools.
type ToolLookup interface {
	Prune(ctx context.Context, budget int, hint string) []core.Tool
	Lookup(name string) (core.Tool, bool)
}

// SkillsIndex prepares approved, trust-tiered instructions before each provider request.
type SkillsIndex interface {
	BuildSystemPromptSections(context.Context) ([]string, error)
}

// Dependencies are the ports a conversation runs against; the caller picks the adapters. Log
// is where every completed message is recorded before the loop acts on it; nil records nothing.
type Dependencies struct {
	Provider core.Provider
	Tools    ToolLookup
	Events   core.EventSink
	Log      core.SessionLog
	Clock    core.Clock
	IDs      core.IDGenerator
	Skills   SkillsIndex
	// IsSpeechEnabled asks the model to mark what to say aloud and publishes each marked span
	// as SpeechRequested. Off, <speak> is ordinary text, so a reply about the tag itself survives.
	IsSpeechEnabled bool
}

// zero defers to the lookup's own exposure limit
const defaultToolBudget = 0

// Tool results the loop produces itself; they go back to the model, which can retry.
const (
	unknownToolResult  = `{"error":"unknown_tool"}`
	toolPanickedResult = `{"error":"tool_panicked"}`
	toolAbortedResult  = `{"error":"tool_aborted"}`
)

// AssistantConversation drives assistant turns against a provider. It depends on the
// core seams only, so the concrete provider, tools, event delivery, clock, and IDs are
// all chosen by the caller.
//
// RunAssistantTurn and Rewind mutate the conversation, so they never overlap: a turn waits for
// the one before it, and a rewind is refused while a turn runs.
type AssistantConversation struct {
	busy sync.Mutex

	provider core.Provider
	tools    ToolLookup
	events   core.EventSink
	log      core.SessionLog
	clock    core.Clock
	ids      core.IDGenerator
	skills   SkillsIndex
	model    string

	isSpeechEnabled bool
	// hasSpokenThisTurn records whether the model marked any speech, so a silent reply gets a fallback.
	hasSpokenThisTurn bool
	maxSteps          int

	// entries is this conversation's copy of what it recorded in the session log; the history
	// the model sees is always rebuilt from it, never kept separately.
	entries []core.SessionEntry
	// turnID is minted when a turn starts and stamped on every event it publishes.
	turnID core.ID
	// isNextStepUntrusted carries untrusted intake into the next turn's first step when the turn
	// ended (budget, error) before any step could follow it (§5.12.4).
	isNextStepUntrusted bool
}

// BuildAssistantConversation returns a conversation that sends every turn to the given
// model and allows at most maxSteps provider calls per turn.
func BuildAssistantConversation(deps Dependencies, model string, maxSteps int) *AssistantConversation {
	return &AssistantConversation{
		provider: deps.Provider,
		tools:    deps.Tools,
		events:   deps.Events,
		log:      deps.Log,
		clock:    deps.Clock,
		ids:      deps.IDs,
		skills:   deps.Skills,
		model:    model,
		maxSteps: maxSteps,

		isSpeechEnabled: deps.IsSpeechEnabled,
	}
}

// stepReply is what one provider call produced.
type stepReply struct {
	text   string
	calls  []core.ToolCall
	usage  core.Usage
	reason core.StopReason
}

// RunAssistantTurn sends one prompt and keeps calling the provider, running the tools it
// asks for, until it answers with text alone or the step budget runs out. The returned
// error reports required work that failed while the caller waited; the assistant's own
// text arrives through the event sink, not the return value.
func (c *AssistantConversation) RunAssistantTurn(ctx context.Context, prompt string) error {
	c.busy.Lock()
	defer c.busy.Unlock()
	c.turnID = c.ids.GenerateID()
	c.hasSpokenThisTurn = false
	userMessage := core.Message{Role: core.RoleUser, Parts: []core.Part{core.TextPart{Text: prompt}}}
	c.events.Publish(ctx, core.UserMessageSubmitted{EventHeader: c.buildEventHeader(), Message: userMessage})
	if err := c.recordMessage(ctx, userMessage); err != nil {
		c.completeTurn(ctx, core.StopError, core.Usage{})
		return err
	}

	var usage core.Usage
	hasUntrustedIntake := c.isNextStepUntrusted
	for step := 0; step < c.maxSteps; step++ {
		stepContext := ctx
		if hasUntrustedIntake {
			stepContext = core.MarkUntrustedStep(ctx)
		}
		reply, err := c.runStep(stepContext, prompt)
		if err != nil {
			c.completeTurn(ctx, core.StopError, usage)
			return err
		}
		usage.InputTokens += reply.usage.InputTokens
		usage.OutputTokens += reply.usage.OutputTokens

		if err := c.recordMessage(ctx, buildAssistantMessage(reply)); err != nil {
			c.completeTurn(ctx, core.StopError, usage)
			return err
		}
		if len(reply.calls) == 0 {
			c.isNextStepUntrusted = false
			if c.isSpeechEnabled && !c.hasSpokenThisTurn {
				if sentence := pickSpokenSentence(reply.text); sentence != "" {
					c.events.Publish(ctx, core.SpeechRequested{EventHeader: c.buildEventHeader(), Text: sentence})
				}
			}
			c.completeTurn(ctx, reply.reason, usage)
			return nil
		}

		results, hasUntrustedOutput, err := c.runToolCalls(stepContext, reply.calls)
		hasUntrustedIntake, c.isNextStepUntrusted = hasUntrustedOutput, hasUntrustedOutput
		err = errors.Join(err, c.recordToolResults(ctx, results))
		if err != nil {
			c.completeTurn(ctx, core.StopError, usage)
			return err
		}
	}

	c.completeTurn(ctx, core.StopBudget, usage)
	return nil
}

// ListTurns reports the turns still in the conversation, as the session log has them.
func (c *AssistantConversation) ListTurns(ctx context.Context) ([]core.TurnSummary, error) {
	if c.log == nil {
		return nil, nil
	}
	return c.log.ListTurns(ctx)
}

// Rewind takes back fromTurn and every later turn, as mode says (/undo). The session log does
// the work and records it; the conversation then applies the same entries to its own copy, so
// the next request leaves out what was rewound, or tells the model which files changed back.
func (c *AssistantConversation) Rewind(ctx context.Context, fromTurn core.ID, mode core.RewindMode) (core.RewindReport, error) {
	if !c.busy.TryLock() {
		return core.RewindReport{}, fmt.Errorf("a turn is running; rewind once it ends: %w", core.ErrInvalid)
	}
	defer c.busy.Unlock()
	if c.log == nil {
		return core.RewindReport{}, fmt.Errorf("this conversation keeps no session log to rewind: %w", core.ErrInvalid)
	}
	report, err := c.log.Rewind(ctx, fromTurn, mode)
	if err != nil {
		return core.RewindReport{}, err
	}

	header := core.EventHeader{ID: c.ids.GenerateID(), At: c.clock.ReportCurrentTime()}
	if mode.IsRevertingFiles() {
		record := core.RewindRecord{FromTurn: fromTurn, Mode: mode, Files: report.Files}
		c.entries = append(c.entries, core.SessionEntry{Kind: core.EntryFilesReverted, Rewind: &record})
		c.events.Publish(ctx, core.FilesReverted{EventHeader: header, FromTurn: fromTurn, Files: report.Files})
	}
	if mode.IsRewindingConversation() {
		record := core.RewindRecord{FromTurn: fromTurn, Mode: mode}
		c.entries = append(c.entries, core.SessionEntry{Kind: core.EntryRewound, Rewind: &record})
		header.ID = c.ids.GenerateID()
		c.events.Publish(ctx, core.ConversationRewound{EventHeader: header, FromTurn: fromTurn, Mode: mode})
	}
	return report, nil
}

// ResetHistory and SelectModel are only called between turns, so they need no lock.

// ResetHistory forgets every earlier message, so the next turn starts a fresh conversation.
// The session log keeps them; only this conversation's copy is dropped.
func (c *AssistantConversation) ResetHistory() {
	c.entries = nil
	c.isNextStepUntrusted = false
}

// SelectModel sends every later turn to the named model.
func (c *AssistantConversation) SelectModel(name string) {
	c.model = name
}

// SelectProvider sends every later turn to provider and model. Unlike SelectModel it can be
// reached from a background command, so it refuses rather than race a running turn.
func (c *AssistantConversation) SelectProvider(provider core.Provider, model string) error {
	if !c.busy.TryLock() {
		return fmt.Errorf("a turn is running; switch provider once it ends: %w", core.ErrInvalid)
	}
	defer c.busy.Unlock()
	c.provider, c.model = provider, model
	return nil
}

// runStep makes exactly one provider call; it exists so the stream closes every step.
func (c *AssistantConversation) runStep(ctx context.Context, hint string) (stepReply, error) {
	var sections []string
	if c.skills != nil {
		var err error
		sections, err = c.skills.BuildSystemPromptSections(ctx)
		if err != nil {
			return stepReply{}, fmt.Errorf("load instructions: %w", err)
		}
	}
	tools := c.buildToolSpecs(ctx, hint)
	request := core.ProviderRequest{
		Model:    c.model,
		System:   buildSystemPrompt(c.clock.ReportCurrentTime(), sections, c.isSpeechEnabled),
		Messages: session.BuildHistory(c.entries),
		Tools:    tools,
	}
	stream, err := c.provider.Chat(ctx, request)
	if err != nil {
		return stepReply{}, err
	}
	defer func() { _ = stream.Close() }()

	return c.consumeStream(ctx, stream)
}

func (c *AssistantConversation) buildToolSpecs(ctx context.Context, hint string) []core.ToolSpec {
	if c.tools == nil {
		return nil
	}
	tools := c.tools.Prune(ctx, defaultToolBudget, hint)
	specs := make([]core.ToolSpec, 0, len(tools))
	for _, tool := range tools {
		specs = append(specs, tool.Describe())
	}
	return specs
}

func (c *AssistantConversation) consumeStream(ctx context.Context, stream core.Stream) (stepReply, error) {
	var text strings.Builder
	var speech speechExtractor
	reply := stepReply{reason: core.StopEndTurn}

	for {
		chunk, err := stream.Next()
		if errors.Is(err, io.EOF) {
			if c.isSpeechEnabled {
				shown, speeches := speech.flush()
				c.publishAssistantText(ctx, shown, speeches)
			}
			reply.text = text.String()
			c.mintMissingCallIDs(reply.calls)
			return reply, nil
		}
		if err != nil {
			return stepReply{}, err
		}

		if chunk.Reasoning != "" {
			c.events.Publish(ctx, core.ThoughtStreamed{EventHeader: c.buildEventHeader(), Text: chunk.Reasoning})
		}
		if chunk.Text != "" {
			text.WriteString(chunk.Text)
			if c.isSpeechEnabled {
				shown, speeches := speech.feed(chunk.Text)
				c.publishAssistantText(ctx, shown, speeches)
			} else {
				c.publishAssistantText(ctx, chunk.Text, nil)
			}
		}
		reply.calls = append(reply.calls, chunk.ToolCalls...)
		if chunk.Usage != nil {
			reply.usage = *chunk.Usage
		}
		if chunk.Finish != "" {
			reply.reason = chunk.Finish
		}
	}
}

// publishAssistantText publishes shown text before the speech it closed, so the transcript
// never lags what is being said.
func (c *AssistantConversation) publishAssistantText(ctx context.Context, shown string, speeches []string) {
	if shown != "" {
		c.events.Publish(ctx, core.AssistantTextStreamed{EventHeader: c.buildEventHeader(), Text: shown})
	}
	for _, speech := range speeches {
		c.hasSpokenThisTurn = true
		c.events.Publish(ctx, core.SpeechRequested{EventHeader: c.buildEventHeader(), Text: speech})
	}
}

// Results are matched to calls by ID, so a provider that omits one still needs one.
func (c *AssistantConversation) mintMissingCallIDs(calls []core.ToolCall) {
	for i := range calls {
		if calls[i].ID == "" {
			calls[i].ID = c.ids.GenerateID()
		}
	}
}

func buildAssistantMessage(reply stepReply) core.Message {
	parts := make([]core.Part, 0, 1+len(reply.calls))
	if reply.text != "" || len(reply.calls) == 0 {
		parts = append(parts, core.TextPart{Text: reply.text})
	}
	for _, call := range reply.calls {
		parts = append(parts, core.ToolCallPart{ToolCall: call})
	}
	return core.Message{Role: core.RoleAssistant, Parts: parts}
}

// runToolCalls preserves order and escalates remaining effects after any untrusted result.
func (c *AssistantConversation) runToolCalls(ctx context.Context, calls []core.ToolCall) ([]core.Part, bool, error) {
	parts := make([]core.Part, 0, len(calls))
	hasUntrustedOutput := false
	for i, call := range calls {
		c.events.Publish(ctx, core.ToolCallStarted{EventHeader: c.buildEventHeader(), Call: call})

		output, err := c.invokeTool(ctx, call)
		if output.IsUntrusted {
			hasUntrustedOutput = true
			ctx = core.MarkUntrustedStep(ctx)
		}
		if err != nil {
			output = core.ToolOutput{Content: toolAbortedResult, Display: err.Error(), IsError: true}
		}
		result := core.ToolResult{CallID: call.ID, Name: call.Name, Content: output.Content, IsError: output.IsError}
		c.events.Publish(ctx, core.ToolCallFinished{EventHeader: c.buildEventHeader(), Result: result, Display: output.Display})
		parts = append(parts, core.ToolResultPart{ToolResult: result})

		if err != nil {
			for _, skipped := range calls[i+1:] {
				parts = append(parts, core.ToolResultPart{ToolResult: core.ToolResult{
					CallID: skipped.ID, Name: skipped.Name, Content: toolAbortedResult, IsError: true,
				}})
			}
			return parts, hasUntrustedOutput, fmt.Errorf("run tool %q: %w", call.Name, err)
		}
	}
	return parts, hasUntrustedOutput, nil
}

// invokeTool turns an unknown name or a panicking tool into an error result for the
// model; only an error the tool itself returns ends the turn.
func (c *AssistantConversation) invokeTool(ctx context.Context, call core.ToolCall) (output core.ToolOutput, err error) {
	tool, isFound := c.lookupTool(call.Name)
	if !isFound {
		return core.ToolOutput{Content: unknownToolResult, Display: fmt.Sprintf("unknown tool %q", call.Name), IsError: true}, nil
	}

	defer func() {
		if recovered := recover(); recovered != nil {
			output = core.ToolOutput{Content: toolPanickedResult, Display: fmt.Sprintf("tool panicked: %v", recovered), IsError: true}
			err = nil
		}
	}()
	return tool.Invoke(core.WithTurnID(core.WithToolCallID(ctx, call.ID), c.turnID), call.Arguments)
}

func (c *AssistantConversation) lookupTool(name string) (core.Tool, bool) {
	if c.tools == nil {
		return nil, false
	}
	return c.tools.Lookup(name)
}

// recordMessage logs a completed message before the conversation acts on it, so the session log
// never misses a message the model was shown or a tool was run for. A message the log refused
// is not kept either, so the conversation and its log agree. It ignores cancellation, because
// what it records has already happened.
func (c *AssistantConversation) recordMessage(ctx context.Context, message core.Message) error {
	entry := c.buildMessageEntry(message)
	if err := c.appendToLog(ctx, entry); err != nil {
		return err
	}
	c.entries = append(c.entries, entry)
	return nil
}

// recordToolResults is recordMessage for the answers to tool calls already in the history. They
// are kept even when the log refuses them, which ends the turn: a history whose tool calls go
// unanswered is rejected by the provider, and every later turn would fail with it.
func (c *AssistantConversation) recordToolResults(ctx context.Context, results []core.Part) error {
	entry := c.buildMessageEntry(core.Message{Role: core.RoleTool, Parts: results})
	c.entries = append(c.entries, entry)
	return c.appendToLog(ctx, entry)
}

func (c *AssistantConversation) buildMessageEntry(message core.Message) core.SessionEntry {
	return core.SessionEntry{Kind: core.EntryMessage, Turn: c.turnID, Message: &message}
}

func (c *AssistantConversation) appendToLog(ctx context.Context, entry core.SessionEntry) error {
	if c.log == nil {
		return nil
	}
	if err := c.log.AppendEntries(context.WithoutCancel(ctx), []core.SessionEntry{entry}); err != nil {
		return fmt.Errorf("record the %s message: %w", entry.Message.Role, err)
	}
	return nil
}

func (c *AssistantConversation) completeTurn(ctx context.Context, reason core.StopReason, usage core.Usage) {
	c.events.Publish(ctx, core.TurnCompleted{EventHeader: c.buildEventHeader(), Reason: reason, Usage: usage})
}

func (c *AssistantConversation) buildEventHeader() core.EventHeader {
	return core.EventHeader{ID: c.ids.GenerateID(), TurnID: c.turnID, At: c.clock.ReportCurrentTime()}
}
