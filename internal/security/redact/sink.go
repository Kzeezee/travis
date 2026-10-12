package redact

import (
	"context"

	"github.com/Kzeezee/travis/internal/core"
)

// WrapSink returns an EventSink that redacts each event with redactor before handing it
// to next, so nothing reaches the bus — and, through it, no subscriber, trace file, or UI —
// unredacted.
func WrapSink(next core.EventSink, redactor Redactor) core.EventSink {
	return sink{next: next, redactor: redactor}
}

type sink struct {
	next     core.EventSink
	redactor Redactor
}

func (s sink) Publish(ctx context.Context, event core.Event) {
	s.next.Publish(ctx, s.redactor.RedactEvent(event))
}
