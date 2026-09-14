// Package chat owns the interactive text-chat flow.
package chat

import (
	"context"
	"errors"
	"io"

	"github.com/Kzeezee/travis/internal/agent/loop"
	"github.com/Kzeezee/travis/internal/core"
	"github.com/Kzeezee/travis/internal/frontends/terminal"
)

type ChatSession struct {
	events                <-chan core.Event
	terminal              *terminal.Terminal
	assistantConversation *loop.AssistantConversation
}

func BuildChatSession(
	events <-chan core.Event,
	chatTerminal *terminal.Terminal,
	assistantConversation *loop.AssistantConversation,
) *ChatSession {
	return &ChatSession{events: events, terminal: chatTerminal, assistantConversation: assistantConversation}
}

// Run reads prompts until input ends, the user quits, or the context is cancelled.
func (s *ChatSession) Run(ctx context.Context) error {
	for {
		prompt, err := s.terminal.ReadPrompt()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		if prompt.Text == "" {
			continue
		}
		if prompt.ShouldQuit {
			return nil
		}

		if err := s.runPromptTurn(ctx, prompt.Text); err != nil {
			if errors.Is(err, context.Canceled) {
				return nil
			}
			s.terminal.RenderTurnError(err)
		}
	}
}

// runPromptTurn runs one prompt through the assistant while rendering events as they arrive.
func (s *ChatSession) runPromptTurn(ctx context.Context, prompt string) error {
	finished := make(chan error, 1)
	go func() { finished <- s.assistantConversation.RunAssistantTurn(ctx, prompt) }()

	for {
		select {
		case event, isEventChannelOpen := <-s.events:
			if !isEventChannelOpen {
				return <-finished
			}
			s.terminal.RenderEvent(event)
		case err := <-finished:
			s.renderPending()
			return err
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

func (s *ChatSession) renderPending() {
	for {
		select {
		case event, isEventChannelOpen := <-s.events:
			if !isEventChannelOpen {
				return
			}
			s.terminal.RenderEvent(event)
		default:
			return
		}
	}
}
