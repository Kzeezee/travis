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

// Runner drives assistant turns against a provider. It depends on the core seams only,
// so the concrete provider, event delivery, and clock are all chosen by the caller.
//
// Run mutates the conversation, so turns must be serialised; the caller decides when a
// new prompt may start.
type Runner struct {
	provider core.Provider
	events   core.EventSink
	clock    core.Clock
	model    string

	history []core.Message
}

// BuildRunner returns a runner that sends every turn to the given model.
func BuildRunner(provider core.Provider, events core.EventSink, clock core.Clock, model string) *Runner {
	return &Runner{provider: provider, events: events, clock: clock, model: model}
}

// Run sends one prompt, streams the reply out as events, and records both sides of the
// exchange. The returned error reports required work that failed while the caller waited;
// the assistant's own text arrives through the event sink, not the return value.
func (r *Runner) Run(ctx context.Context, prompt string) error {
	user := core.Message{Role: core.RoleUser, Parts: []core.Part{core.TextPart{Text: prompt}}}
	r.history = append(r.history, user)
	r.events.Publish(ctx, core.UserMessageSubmitted{EventHeader: r.buildEventHeader(), Message: user})

	stream, err := r.provider.Chat(ctx, core.ProviderRequest{Model: r.model, Messages: r.history})
	if err != nil {
		r.events.Publish(ctx, core.TurnCompleted{EventHeader: r.buildEventHeader(), Reason: core.StopError})
		return err
	}
	defer func() { _ = stream.Close() }()

	reply, usage, reason, err := r.consumeStream(ctx, stream)
	if err != nil {
		r.events.Publish(ctx, core.TurnCompleted{EventHeader: r.buildEventHeader(), Reason: core.StopError})
		return err
	}

	assistant := core.Message{Role: core.RoleAssistant, Parts: []core.Part{core.TextPart{Text: reply}}}
	r.history = append(r.history, assistant)
	r.events.Publish(ctx, core.TurnCompleted{EventHeader: r.buildEventHeader(), Reason: reason, Usage: usage})
	return nil
}

func (r *Runner) consumeStream(ctx context.Context, stream core.Stream) (string, core.Usage, core.StopReason, error) {
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
			r.events.Publish(ctx, core.AssistantTextStreamed{EventHeader: r.buildEventHeader(), Text: chunk.Text})
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
func (r *Runner) buildEventHeader() core.EventHeader {
	return core.EventHeader{At: r.clock.ReportCurrentTime()}
}
