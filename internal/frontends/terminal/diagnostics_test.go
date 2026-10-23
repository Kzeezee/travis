package terminal

import (
	"strings"
	"testing"
)

func TestDiagnosticsWriterCapturesWholeStrippedLinesOnlyWhileTheChatRuns(t *testing.T) {
	var fallback strings.Builder
	writer := BuildDiagnosticsWriter(&fallback)

	_, _ = writer.Write([]byte("before\n"))
	lines := writer.startCapture()
	_, _ = writer.Write([]byte("travis: mcp server \x1b[31m\"probe\"\x1b[0m did "))
	_, _ = writer.Write([]byte("not start\n\nsecond\nunfinished"))
	writer.stopCapture()
	_, _ = writer.Write([]byte("after\n"))

	for _, want := range []string{`travis: mcp server "probe" did not start`, "second"} {
		if got := <-lines; got != want {
			t.Fatalf("captured %q, want %q", got, want)
		}
	}
	if len(lines) != 0 {
		t.Fatalf("captured %d extra lines", len(lines))
	}
	if got, want := fallback.String(), "before\nunfinished\nafter\n"; got != want {
		t.Fatalf("fallback got %q, want %q", got, want)
	}
}

func TestDiagnosticLineShowsAsANoticeInsteadOfOverTheScreen(t *testing.T) {
	m := buildTestModel(ChatActions{})
	m.diagnostics = make(chan string)

	m, cmd := updateModel(m, diagnosticLineMsg{line: `travis: mcp server "probe" did not start`})

	if cmd == nil {
		t.Fatal("the chat must keep listening for diagnostics")
	}
	if view := renderPlainView(m); !strings.Contains(view, `mcp server "probe" did not start`) || strings.Contains(view, "travis: mcp") {
		t.Fatalf("diagnostic not shown as a notice:\n%s", view)
	}
}
