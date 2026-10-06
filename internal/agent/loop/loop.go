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

	"github.com/Kzeezee/travis/internal/core"
)

// ToolLookup is the loop's view of the registry: what to offer the model this step,
// and how to find what it asked for. A nil ToolLookup means no tools.
type ToolLookup interface {
	Prune(ctx context.Context, budget int, hint string) []core.Tool
	Lookup(name string) (core.Tool, bool)
}

// Dependencies are the ports a conversation runs against; the caller picks the adapters.
type Dependencies struct {
	Provider core.Provider
	Tools    ToolLookup
	Events   core.EventSink
	Clock    core.Clock
	IDs      core.IDGenerator
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
// RunAssistantTurn mutates the conversation, so turns must be serialised; the caller
// decides when a new prompt may start.
type AssistantConversation struct {
	provider core.Provider
	tools    ToolLookup
	events   core.EventSink
	clock    core.Clock
	ids      core.IDGenerator
	model    string
	maxSteps int

	history []core.Message
	// turnID is minted when a turn starts and stamped on every event it publishes.
	turnID core.ID
}

// BuildAssistantConversation returns a conversation that sends every turn to the given
// model and allows at most maxSteps provider calls per turn.
func BuildAssistantConversation(deps Dependencies, model string, maxSteps int) *AssistantConversation {
	return &AssistantConversation{
		provider: deps.Provider,
		tools:    deps.Tools,
		events:   deps.Events,
		clock:    deps.Clock,
		ids:      deps.IDs,
		model:    model,
		maxSteps: maxSteps,
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
	c.turnID = c.ids.GenerateID()
	userMessage := core.Message{Role: core.RoleUser, Parts: []core.Part{core.TextPart{Text: prompt}}}
	c.history = append(c.history, userMessage)
	c.events.Publish(ctx, core.UserMessageSubmitted{EventHeader: c.buildEventHeader(), Message: userMessage})

	var usage core.Usage
	for step := 0; step < c.maxSteps; step++ {
		reply, err := c.runStep(ctx, prompt)
		if err != nil {
			c.completeTurn(ctx, core.StopError, usage)
			return err
		}
		usage.InputTokens += reply.usage.InputTokens
		usage.OutputTokens += reply.usage.OutputTokens

		c.history = append(c.history, buildAssistantMessage(reply))
		if len(reply.calls) == 0 {
			c.completeTurn(ctx, reply.reason, usage)
			return nil
		}

		results, err := c.runToolCalls(ctx, reply.calls)
		c.history = append(c.history, core.Message{Role: core.RoleTool, Parts: results})
		if err != nil {
			c.completeTurn(ctx, core.StopError, usage)
			return err
		}
	}

	c.completeTurn(ctx, core.StopBudget, usage)
	return nil
}

// runStep makes exactly one provider call; it exists so the stream closes every step.
func (c *AssistantConversation) runStep(ctx context.Context, hint string) (stepReply, error) {
	tools := c.buildToolSpecs(ctx, hint)
	request := core.ProviderRequest{
		Model:    c.model,
		System:   buildSystemPrompt(c.clock.ReportCurrentTime()),
		Messages: c.history,
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
	reply := stepReply{reason: core.StopEndTurn}

	for {
		chunk, err := stream.Next()
		if errors.Is(err, io.EOF) {
			reply.text = text.String()
			c.mintMissingCallIDs(reply.calls)
			return reply, nil
		}
		if err != nil {
			return stepReply{}, err
		}

		if chunk.Text != "" {
			text.WriteString(chunk.Text)
			c.events.Publish(ctx, core.AssistantTextStreamed{EventHeader: c.buildEventHeader(), Text: chunk.Text})
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

// runToolCalls dispatches sequentially: every Phase 1 tool is read-only, so parallelism
// buys little yet, and order stays deterministic. Every call gets a result, even after a
// failure, because the provider rejects a history with unanswered calls.
func (c *AssistantConversation) runToolCalls(ctx context.Context, calls []core.ToolCall) ([]core.Part, error) {
	parts := make([]core.Part, 0, len(calls))
	for i, call := range calls {
		c.events.Publish(ctx, core.ToolCallStarted{EventHeader: c.buildEventHeader(), Call: call})

		output, err := c.invokeTool(ctx, call)
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
			return parts, fmt.Errorf("run tool %q: %w", call.Name, err)
		}
	}
	return parts, nil
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
	return tool.Invoke(ctx, call.Arguments)
}

func (c *AssistantConversation) lookupTool(name string) (core.Tool, bool) {
	if c.tools == nil {
		return nil, false
	}
	return c.tools.Lookup(name)
}

func (c *AssistantConversation) completeTurn(ctx context.Context, reason core.StopReason, usage core.Usage) {
	c.events.Publish(ctx, core.TurnCompleted{EventHeader: c.buildEventHeader(), Reason: reason, Usage: usage})
}

func (c *AssistantConversation) buildEventHeader() core.EventHeader {
	return core.EventHeader{ID: c.ids.GenerateID(), TurnID: c.turnID, At: c.clock.ReportCurrentTime()}
}
