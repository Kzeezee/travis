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
