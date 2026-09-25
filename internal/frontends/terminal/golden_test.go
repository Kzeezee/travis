package terminal

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/charmbracelet/x/exp/teatest/v2"

	"github.com/Kzeezee/travis/internal/core"
)

// Regenerate with: go test ./internal/frontends/terminal -run TestGoldenFrames -update
func TestGoldenFrames(t *testing.T) {
	script := []core.Event{
		core.UserMessageSubmitted{},
		core.AssistantTextStreamed{Text: "Let me look."},
		core.ToolCallStarted{Call: core.ToolCall{ID: "1", Name: "list_dir"}},
		core.ToolCallFinished{Result: core.ToolResult{CallID: "1", Name: "list_dir"}},
		core.ToolCallStarted{Call: core.ToolCall{ID: "2", Name: "read_file"}},
		core.ToolCallFinished{Result: core.ToolResult{
			CallID: "2", Name: "read_file", IsError: true,
			Content: "permission denied: /home/kez/notes/ideas.md",
		}},
		core.AssistantTextStreamed{Text: "You have groceries.md, cs464.md and ideas.md (which I couldn't open)."},
		core.TurnCompleted{Reason: core.StopEndTurn},
	}

	for _, width := range []int{50, 80} {
		t.Run(fmt.Sprintf("width_%d", width), func(t *testing.T) {
			// Unbuffered, so each send waits for the listener; the trailing event can only be
			// received once every scripted event has been rendered.
			events := make(chan core.Event)
			runTurn := func(ctx context.Context, _ string) error {
				for _, event := range append(script, core.TurnCompleted{}) {
					select {
					case events <- event:
					case <-ctx.Done():
						return ctx.Err()
					}
				}
				return nil
			}

			model := buildChatModel(context.Background(), events, runTurn, "gpt-4o-mini")
			tm := teatest.NewTestModel(t, model, teatest.WithInitialTermSize(width, 16))
			tm.Type("what's in my notes folder?")
			tm.Send(enterKey)
			tm.Send(inputEndedMsg{}) // quit once the turn finishes

			final := tm.FinalModel(t, teatest.WithFinalTimeout(5*time.Second)).(chatModel)
			teatest.RequireEqualOutput(t, []byte(renderPlainView(final)))
		})
	}
}
