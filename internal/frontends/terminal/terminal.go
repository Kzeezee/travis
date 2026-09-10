// Package terminal is the outer adapter for the text frontend: it collects user intent,
// sends it inward, and renders observed facts. It knows nothing about providers, tools,
// policy, or the agent loop.
package terminal

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/Kzeezee/travis/internal/core"
)

const (
	promptMarker  = "\n› "
	quitCommand   = "/quit"
	errorTemplate = "\nerror: %v\n"
)

// SubmitPrompt is the port the frontend uses to send user intent inward. It returns when
// the turn has ended; the assistant's text arrives separately, as events.
type SubmitPrompt func(ctx context.Context, prompt string) error

// App reads prompts from in, submits them, and renders the resulting events to out.
type App struct {
	events <-chan core.Event
	submit SubmitPrompt
	in     io.Reader
	out    io.Writer
}

// BuildApp returns a frontend bound to one event subscription and one command port.
func BuildApp(events <-chan core.Event, submit SubmitPrompt, in io.Reader, out io.Writer) *App {
	return &App{events: events, submit: submit, in: in, out: out}
}

// Run reads prompts until the input ends, the user quits, or the context is cancelled.
// A failed turn is reported to the user and the session continues.
func (a *App) Run(ctx context.Context) error {
	input := bufio.NewScanner(a.in)

	for {
		a.write(promptMarker)
		if !input.Scan() {
			a.write("\n")
			return input.Err()
		}

		prompt := strings.TrimSpace(input.Text())
		if prompt == "" {
			continue
		}
		if prompt == quitCommand {
			return nil
		}

		if err := a.runTurn(ctx, prompt); err != nil {
			if errors.Is(err, context.Canceled) {
				return nil
			}
			a.write(fmt.Sprintf(errorTemplate, err))
		}
	}
}

// runTurn submits the prompt and renders events until the turn returns. The submission
// runs alongside rendering because the events describing it arrive while it is still
// in flight.
func (a *App) runTurn(ctx context.Context, prompt string) error {
	finished := make(chan error, 1)
	go func() { finished <- a.submit(ctx, prompt) }()

	for {
		select {
		case event, open := <-a.events:
			if !open {
				return <-finished
			}
			a.render(event)
		case err := <-finished:
			a.renderPending()
			return err
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

// renderPending drains facts already published before the turn returned.
func (a *App) renderPending() {
	for {
		select {
		case event, open := <-a.events:
			if !open {
				return
			}
			a.render(event)
		default:
			return
		}
	}
}

func (a *App) render(event core.Event) {
	switch fact := event.(type) {
	case core.AssistantTextStreamed:
		a.write(fact.Text)
	case core.TurnCompleted:
		a.write("\n")
	case core.UserMessageSubmitted, core.ToolCallStarted, core.ToolCallFinished:
		// The prompt is already on screen, and tools arrive with the registry in 1C.
	}
}

func (a *App) write(text string) {
	_, _ = io.WriteString(a.out, text)
}
