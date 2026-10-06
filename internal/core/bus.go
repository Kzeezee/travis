package core

import (
	"context"
	"sync"
)

// defaultSubscriberBuffer follows DESIGN.md 5.1; a subscriber this far behind is
// already failing to keep up, and buffering more would only delay the drop.
const defaultSubscriberBuffer = 1024

// Bus fans published facts out to subscribers. A slow subscriber must never stall a
// publisher, so delivery to a full subscriber drops the event and logs it rather than
// blocking or queueing (Code-Conventions.md, async event delivery).
type Bus struct {
	logger Logger
	buffer int

	mu          sync.Mutex
	subscribers map[int]*subscription
	nextID      int
	closed      bool
	// ponytail: one count across all subscribers, so a slow chat view also counts against the trace; per-subscriber counts if that misleads
	droppedCount int
}

type subscription struct {
	events chan Event
	accept func(Event) bool
}

// BuildEventBus returns a bus that gives each subscriber its own buffer of the given
// size. A size below one falls back to the default.
func BuildEventBus(logger Logger, bufferPerSubscriber int) *Bus {
	if bufferPerSubscriber < 1 {
		bufferPerSubscriber = defaultSubscriberBuffer
	}
	return &Bus{
		logger:      logger,
		buffer:      bufferPerSubscriber,
		subscribers: make(map[int]*subscription),
	}
}

// Publish delivers a fact to every accepting subscriber. Delivery never blocks, so the
// context governs the caller's work rather than this fan-out: an event describes
// something that already happened and cancelling does not un-happen it.
func (b *Bus) Publish(_ context.Context, event Event) {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.closed {
		return
	}
	for _, subscriber := range b.subscribers {
		if subscriber.accept != nil && !subscriber.accept(event) {
			continue
		}
		select {
		case subscriber.events <- event:
		default:
			b.droppedCount++
			b.logger.LogWarnMsg("event dropped", "type", string(event.ReportEventType()))
		}
	}
}

// ReportDroppedEventCount reports how many deliveries were dropped because a subscriber was full.
func (b *Bus) ReportDroppedEventCount() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.droppedCount
}

// Subscribe returns a channel of accepted events and a function that cancels the
// subscription and closes the channel. A nil filter accepts every event. Filters run
// on the publishing goroutine, so they must be cheap and must not block.
func (b *Bus) Subscribe(filter func(Event) bool) (<-chan Event, func()) {
	b.mu.Lock()
	defer b.mu.Unlock()

	events := make(chan Event, b.buffer)
	if b.closed {
		close(events)
		return events, func() {}
	}

	id := b.nextID
	b.nextID++
	b.subscribers[id] = &subscription{events: events, accept: filter}

	return events, func() { b.unsubscribe(id) }
}

// Close ends every subscription so observers draining the bus can finish.
func (b *Bus) Close() {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.closed {
		return
	}
	b.closed = true
	for id, subscriber := range b.subscribers {
		close(subscriber.events)
		delete(b.subscribers, id)
	}
}

func (b *Bus) unsubscribe(id int) {
	b.mu.Lock()
	defer b.mu.Unlock()

	subscriber, ok := b.subscribers[id]
	if !ok {
		return
	}
	delete(b.subscribers, id)
	close(subscriber.events)
}
