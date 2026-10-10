package redact

import (
	"context"

	"github.com/Kzeezee/travis/internal/core"
)

// Sink wraps an EventSink and redacts every event before passing it on, so nothing
// reaches the bus — and, through it, no subscriber, trace file, or UI — unredacted.
type Sink struct {
	next     core.EventSink
	redactor Redactor
}

var _ core.EventSink = Sink{}

// WrapSink returns an EventSink that redacts each event with redactor before handing it
// to next.
func WrapSink(next core.EventSink, redactor Redactor) Sink {
	return Sink{next: next, redactor: redactor}
}

func (s Sink) Publish(ctx context.Context, event core.Event) {
	s.next.Publish(ctx, s.redactor.RedactEvent(event))
}
