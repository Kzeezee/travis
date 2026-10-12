package redact_test

import (
	"context"
	"testing"

	"github.com/Kzeezee/travis/internal/core"
	"github.com/Kzeezee/travis/internal/security/redact"
)

type recordingSink struct {
	received []core.Event
}

func (s *recordingSink) Publish(_ context.Context, event core.Event) {
	s.received = append(s.received, event)
}

func TestSinkPublishAlwaysDeliversTheRedactedForm(t *testing.T) {
	next := &recordingSink{}
	sink := redact.WrapSink(next, redact.New("topsecret"))

	sink.Publish(context.Background(), core.AssistantTextStreamed{
		EventHeader: core.EventHeader{ID: "evt-1"},
		Text:        "contains topsecret",
	})

	if len(next.received) != 1 {
		t.Fatalf("next received %d events, want 1", len(next.received))
	}
	got, ok := next.received[0].(core.AssistantTextStreamed)
	if !ok {
		t.Fatalf("next received %T, want core.AssistantTextStreamed", next.received[0])
	}
	if got.Text != "contains "+redact.Placeholder {
		t.Fatalf("Text = %q, want the secret scrubbed", got.Text)
	}
}

type recordingSessionLog struct {
	entries []core.SessionEntry
}

func (l *recordingSessionLog) AppendEntries(_ context.Context, entries []core.SessionEntry) error {
	l.entries = append(l.entries, entries...)
	return nil
}

func (l *recordingSessionLog) ListTurns(context.Context) ([]core.TurnSummary, error) { return nil, nil }

func TestSessionLogRecordsOnlyTheRedactedForm(t *testing.T) {
	next := &recordingSessionLog{}
	original := core.Message{Role: core.RoleTool, Parts: []core.Part{
		core.TextPart{Text: "key topsecret"},
		core.ToolResultPart{ToolResult: core.ToolResult{CallID: "c1", Content: "TOKEN=topsecret"}},
	}}
	entry := core.SessionEntry{Kind: core.EntryMessage, Turn: "t1", Message: &original}

	if err := redact.WrapSessionLog(next, redact.New("topsecret")).AppendEntries(context.Background(), []core.SessionEntry{entry}); err != nil {
		t.Fatal(err)
	}
	recorded := next.entries[0].Message.Parts
	if text := recorded[0].(core.TextPart).Text; text != "key "+redact.Placeholder {
		t.Fatalf("text = %q, want the secret scrubbed", text)
	}
	if content := recorded[1].(core.ToolResultPart).ToolResult.Content; content != "TOKEN="+redact.Placeholder {
		t.Fatalf("tool result = %q, want the secret scrubbed", content)
	}
	if original.Parts[0].(core.TextPart).Text != "key topsecret" {
		t.Fatal("redaction changed the caller's own message, which the model still needs")
	}
}
