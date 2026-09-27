package terminal_test

import (
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/Kzeezee/travis/internal/core"
	"github.com/Kzeezee/travis/internal/frontends/terminal"
)

func TestReadPromptReportsTrimmedText(t *testing.T) {
	var out strings.Builder
	chatTerminal := terminal.BuildTerminal(strings.NewReader("  hello  \n"), &out)

	prompt, err := chatTerminal.ReadPrompt()

	if err != nil {
		t.Fatalf("ReadPrompt returned %v, want nil", err)
	}
	if prompt.Text != "hello" {
		t.Fatalf("got prompt %q, want hello", prompt.Text)
	}
	if prompt.ShouldQuit {
		t.Fatal("prompt should not request quit")
	}
	if !strings.Contains(out.String(), "› ") {
		t.Fatalf("got output %q, want prompt marker", out.String())
	}
}

func TestReadPromptReportsQuit(t *testing.T) {
	chatTerminal := terminal.BuildTerminal(strings.NewReader("/quit\n"), &strings.Builder{})

	prompt, err := chatTerminal.ReadPrompt()

	if err != nil {
		t.Fatalf("ReadPrompt returned %v, want nil", err)
	}
	if !prompt.ShouldQuit {
		t.Fatal("prompt should request quit")
	}
}

func TestReadPromptReportsInputEnd(t *testing.T) {
	var out strings.Builder
	chatTerminal := terminal.BuildTerminal(strings.NewReader(""), &out)

	_, err := chatTerminal.ReadPrompt()

	if !errors.Is(err, io.EOF) {
		t.Fatalf("got %v, want io.EOF", err)
	}
	if !strings.HasSuffix(out.String(), "\n") {
		t.Fatalf("got output %q, want trailing newline on input end", out.String())
	}
}

func TestRenderEventWritesAssistantTextAndCompletion(t *testing.T) {
	var out strings.Builder
	chatTerminal := terminal.BuildTerminal(strings.NewReader(""), &out)

	chatTerminal.RenderEvent(core.AssistantTextStreamed{Text: "hello"})
	chatTerminal.RenderEvent(core.TurnCompleted{Reason: core.StopEndTurn})

	if got := out.String(); got != "hello\n" {
		t.Fatalf("got output %q, want streamed text and newline", got)
	}
}

func TestRenderEventWritesToolCallStartedAndFinished(t *testing.T) {
	var out strings.Builder
	chatTerminal := terminal.BuildTerminal(strings.NewReader(""), &out)

	chatTerminal.RenderEvent(core.ToolCallStarted{Call: core.ToolCall{Name: "fs.read"}})
	chatTerminal.RenderEvent(core.ToolCallFinished{Display: "read AGENTS.md"})

	if got, want := out.String(), "\n· fs.read\n  ✓ read AGENTS.md\n"; got != want {
		t.Fatalf("got output %q, want %q", got, want)
	}
}

func TestRenderEventWritesFailedToolCall(t *testing.T) {
	var out strings.Builder
	chatTerminal := terminal.BuildTerminal(strings.NewReader(""), &out)

	chatTerminal.RenderEvent(core.ToolCallFinished{
		Result:  core.ToolResult{IsError: true},
		Display: "fs.read: file_not_found",
	})

	if got, want := out.String(), "  ✗ fs.read: file_not_found\n"; got != want {
		t.Fatalf("got output %q, want %q", got, want)
	}
}

func TestRenderTurnErrorWritesError(t *testing.T) {
	var out strings.Builder
	chatTerminal := terminal.BuildTerminal(strings.NewReader(""), &out)

	chatTerminal.RenderTurnError(errors.New("provider exhausted"))

	if !strings.Contains(out.String(), "error: provider exhausted") {
		t.Fatalf("got output %q, want rendered error", out.String())
	}
}
