// Package loop owns one assistant turn: it asks the provider, relays what happened as
// observable facts, and keeps the conversation so the next prompt has context.
package loop

import (
	"context"
	"errors"
	"io"
	"strings"

	"github.com/Kzeezee/travis/internal/core"
)

// AssistantConversation drives assistant turns against a provider. It depends on the
// core seams only, so the concrete provider, event delivery, and clock are all chosen
// by the caller.
//
// RunAssistantTurn mutates the conversation, so turns must be serialised; the caller
// decides when a new prompt may start.
type AssistantConversation struct {
	provider core.Provider
	events   core.EventSink
	clock    core.Clock
	model    string

	history []core.Message
}

// BuildAssistantConversation returns a conversation that sends every turn to the given model.
func BuildAssistantConversation(
	provider core.Provider,
	events core.EventSink,
	clock core.Clock,
	model string,
) *AssistantConversation {
	return &AssistantConversation{provider: provider, events: events, clock: clock, model: model}
}

// RunAssistantTurn sends one prompt, streams the reply out as events, and records both
// sides of the exchange. The returned error reports required work that failed while the
// caller waited; the assistant's own text arrives through the event sink, not the return value.
func (c *AssistantConversation) RunAssistantTurn(ctx context.Context, prompt string) error {
	userMessage := core.Message{Role: core.RoleUser, Parts: []core.Part{core.TextPart{Text: prompt}}}
	c.history = append(c.history, userMessage)
	c.events.Publish(ctx, core.UserMessageSubmitted{EventHeader: c.buildEventHeader(), Message: userMessage})

	stream, err := c.provider.Chat(ctx, core.ProviderRequest{Model: c.model, Messages: c.history})
	if err != nil {
		c.events.Publish(ctx, core.TurnCompleted{EventHeader: c.buildEventHeader(), Reason: core.StopError})
		return err
	}
	defer func() { _ = stream.Close() }()

	reply, usage, reason, err := c.consumeStream(ctx, stream)
	if err != nil {
		c.events.Publish(ctx, core.TurnCompleted{EventHeader: c.buildEventHeader(), Reason: core.StopError})
		return err
	}

	assistantMessage := core.Message{Role: core.RoleAssistant, Parts: []core.Part{core.TextPart{Text: reply}}}
	c.history = append(c.history, assistantMessage)
	c.events.Publish(ctx, core.TurnCompleted{EventHeader: c.buildEventHeader(), Reason: reason, Usage: usage})
	return nil
}

func (c *AssistantConversation) consumeStream(ctx context.Context, stream core.Stream) (string, core.Usage, core.StopReason, error) {
	var reply strings.Builder
	var usage core.Usage
	reason := core.StopEndTurn

	for {
		chunk, err := stream.Next()
		if errors.Is(err, io.EOF) {
			return reply.String(), usage, reason, nil
		}
		if err != nil {
			return "", core.Usage{}, core.StopError, err
		}

		if chunk.Text != "" {
			reply.WriteString(chunk.Text)
			c.events.Publish(ctx, core.AssistantTextStreamed{EventHeader: c.buildEventHeader(), Text: chunk.Text})
		}
		if chunk.Usage != nil {
			usage = *chunk.Usage
		}
		if chunk.Finish != "" {
			reason = chunk.Finish
		}
	}
}

// Event IDs stay empty until a generator exists; core deliberately declares core.ID
// without generating it, so nothing here can mint one yet.
func (c *AssistantConversation) buildEventHeader() core.EventHeader {
	return core.EventHeader{At: c.clock.ReportCurrentTime()}
}
