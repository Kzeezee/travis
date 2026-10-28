package terminal

import (
	"bytes"
	"io"
	"strings"
	"sync"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

// capturedDiagnosticCapacity bounds the diagnostic lines waiting for the chat to show them.
const capturedDiagnosticCapacity = 64

// DiagnosticsWriter shows diagnostic lines as transcript notices while the chat runs, because a raw
// write to the terminal draws over the screen Bubble Tea owns; outside the chat it writes to fallback.
type DiagnosticsWriter struct {
	mu       sync.Mutex
	fallback io.Writer
	lines    chan string // non-nil only while the chat captures
	partial  []byte
}

func BuildDiagnosticsWriter(fallback io.Writer) *DiagnosticsWriter {
	return &DiagnosticsWriter{fallback: fallback}
}

func (w *DiagnosticsWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.lines == nil {
		return w.fallback.Write(p)
	}
	w.partial = append(w.partial, p...)
	for {
		end := bytes.IndexByte(w.partial, '\n')
		if end < 0 {
			return len(p), nil
		}
		// Stripped so no escape sequence from a child's stderr reaches the screen.
		line := strings.TrimSpace(ansi.Strip(string(w.partial[:end])))
		w.partial = w.partial[end+1:]
		if line == "" {
			continue
		}
		select {
		case w.lines <- line:
		default: // ponytail: drops lines when the chat is far behind; count them if that's ever seen
		}
	}
}

func (w *DiagnosticsWriter) startCapture() <-chan string {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.lines = make(chan string, capturedDiagnosticCapacity)
	return w.lines
}

// stopCapture sends later writes, and any unfinished line, to fallback again.
func (w *DiagnosticsWriter) stopCapture() {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.lines = nil
	if len(w.partial) > 0 {
		_, _ = w.fallback.Write(append(w.partial, '\n'))
		w.partial = nil
	}
}

type diagnosticLineMsg struct{ line string }

func listenForDiagnostics(lines <-chan string) tea.Cmd {
	if lines == nil {
		return nil
	}
	return func() tea.Msg {
		return diagnosticLineMsg{line: <-lines}
	}
}
