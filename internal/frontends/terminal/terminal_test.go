package terminal_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Kzeezee/travis/internal/core"
	"github.com/Kzeezee/travis/internal/frontends/terminal"
)

func TestAppSubmitsEachPromptAndRendersTheStreamedReply(t *testing.T) {
	events := make(chan core.Event, 8)
	var submitted []string
	submit := func(_ context.Context, prompt string) error {
		submitted = append(submitted, prompt)
		events <- core.AssistantTextStreamed{Text: "echo:" + prompt}
		events <- core.TurnCompleted{Reason: core.StopEndTurn}
		return nil
	}

	var out strings.Builder
	app := terminal.BuildApp(events, submit, strings.NewReader("first\n\n  second  \n"), &out)

	if err := app.Run(context.Background()); err != nil {
		t.Fatalf("Run returned %v, want nil", err)
	}
	if len(submitted) != 2 || submitted[0] != "first" || submitted[1] != "second" {
		t.Fatalf("got prompts %v, want trimmed first and second with the blank line skipped", submitted)
	}
	rendered := out.String()
	if !strings.Contains(rendered, "echo:first") || !strings.Contains(rendered, "echo:second") {
		t.Fatalf("got output %q, want both replies", rendered)
	}
}

func TestAppReportsAFailedTurnAndKeepsGoing(t *testing.T) {
	events := make(chan core.Event, 4)
	attempts := 0
	submit := func(_ context.Context, _ string) error {
		attempts++
		if attempts == 1 {
			return errors.New("provider exhausted")
		}
		events <- core.TurnCompleted{Reason: core.StopEndTurn}
		return nil
	}

	var out strings.Builder
	app := terminal.BuildApp(events, submit, strings.NewReader("one\ntwo\n"), &out)

	if err := app.Run(context.Background()); err != nil {
		t.Fatalf("Run returned %v, want nil", err)
	}
	if attempts != 2 {
		t.Fatalf("got %d attempts, want the session to continue after a failure", attempts)
	}
	if !strings.Contains(out.String(), "error: provider exhausted") {
		t.Fatalf("got output %q, want the failure reported", out.String())
	}
}

func TestAppStopsOnQuitWithoutSubmitting(t *testing.T) {
	submit := func(_ context.Context, _ string) error {
		t.Fatal("quit must not reach the agent loop")
		return nil
	}

	var out strings.Builder
	app := terminal.BuildApp(make(chan core.Event), submit, strings.NewReader("/quit\nignored\n"), &out)

	if err := app.Run(context.Background()); err != nil {
		t.Fatalf("Run returned %v, want nil", err)
	}
}

func TestAppStopsWhenInputEnds(t *testing.T) {
	var out strings.Builder
	app := terminal.BuildApp(make(chan core.Event), func(context.Context, string) error { return nil }, strings.NewReader(""), &out)

	if err := app.Run(context.Background()); err != nil {
		t.Fatalf("Run returned %v, want nil", err)
	}
}
