// Package terminal is the outer adapter for the text frontend: it collects user intent
// and renders observed facts. It knows nothing about providers, tools, policy, or the agent loop.
package terminal

import (
	"bufio"
	"fmt"
	"io"
	"strings"

	"github.com/Kzeezee/travis/internal/core"
)

const (
	promptMarker         = "\n› "
	quitCommand          = "/quit"
	errorTemplate        = "\nerror: %v\n"
	toolStartedTemplate  = "\n· %s\n"
	toolFinishedTemplate = "  ✓ %s\n"
	toolFailedTemplate   = "  ✗ %s\n"
)

type Prompt struct {
	Text       string
	ShouldQuit bool
}

// Terminal owns terminal input and output effects for chat.
type Terminal struct {
	input *bufio.Scanner
	out   io.Writer
}

// BuildTerminal returns a terminal adapter bound to the given input and output streams.
func BuildTerminal(in io.Reader, out io.Writer) *Terminal {
	return &Terminal{input: bufio.NewScanner(in), out: out}
}

// ReadPrompt reads one prompt from the terminal. io.EOF reports normal input end.
func (t *Terminal) ReadPrompt() (Prompt, error) {
	t.write(promptMarker)
	if !t.input.Scan() {
		t.write("\n")
		if err := t.input.Err(); err != nil {
			return Prompt{}, err
		}
		return Prompt{}, io.EOF
	}

	text := strings.TrimSpace(t.input.Text())
	return Prompt{Text: text, ShouldQuit: text == quitCommand}, nil
}

func (t *Terminal) RenderEvent(event core.Event) {
	switch fact := event.(type) {
	case core.AssistantTextStreamed:
		t.write(fact.Text)
	case core.TurnCompleted:
		t.write("\n")
	case core.ToolCallStarted:
		t.write(fmt.Sprintf(toolStartedTemplate, fact.Call.Name))
	case core.ToolCallFinished:
		template := toolFinishedTemplate
		if fact.Result.IsError {
			template = toolFailedTemplate
		}
		t.write(fmt.Sprintf(template, fact.Display))
	case core.UserMessageSubmitted:
		// The prompt is already on screen.
	}
}

func (t *Terminal) RenderTurnError(err error) {
	t.write(fmt.Sprintf(errorTemplate, err))
}

func (t *Terminal) write(text string) {
	_, _ = io.WriteString(t.out, text)
}
