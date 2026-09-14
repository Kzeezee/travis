package core_test

import (
	"context"
	"strings"
	"testing"

	"github.com/Kzeezee/travis/internal/core"
	"github.com/Kzeezee/travis/internal/log"
)

func TestBusPublishReachesEverySubscriber(t *testing.T) {
	bus := core.BuildEventBus(log.BuildNopLogger(), 4)
	defer bus.Close()

	first, cancelFirst := bus.Subscribe(nil)
	defer cancelFirst()
	second, cancelSecond := bus.Subscribe(nil)
	defer cancelSecond()

	bus.Publish(context.Background(), core.AssistantTextStreamed{Text: "hello"})

	for _, events := range []<-chan core.Event{first, second} {
		got, ok := <-events
		if !ok {
			t.Fatal("subscriber channel closed before delivery")
		}
		streamed, ok := got.(core.AssistantTextStreamed)
		if !ok {
			t.Fatalf("got %T, want core.AssistantTextStreamed", got)
		}
		if streamed.Text != "hello" {
			t.Fatalf("got text %q, want %q", streamed.Text, "hello")
		}
	}
}

func TestBusSubscribeFilterSkipsUnwantedEvents(t *testing.T) {
	bus := core.BuildEventBus(log.BuildNopLogger(), 4)
	defer bus.Close()

	onlyCompletions := func(event core.Event) bool {
		return event.ReportEventType() == core.EventTurnCompleted
	}
	events, cancel := bus.Subscribe(onlyCompletions)
	defer cancel()

	bus.Publish(context.Background(), core.AssistantTextStreamed{Text: "ignored"})
	bus.Publish(context.Background(), core.TurnCompleted{Reason: core.StopEndTurn})

	got := <-events
	if got.ReportEventType() != core.EventTurnCompleted {
		t.Fatalf("got %s, want %s", got.ReportEventType(), core.EventTurnCompleted)
	}
	select {
	case extra := <-events:
		t.Fatalf("filtered subscriber received %T", extra)
	default:
	}
}

func TestBusPublishDropsAndLogsWhenSubscriberIsFull(t *testing.T) {
	var logged strings.Builder
	bus := core.BuildEventBus(log.BuildTextLogger(&logged, log.LevelWarn), 1)
	defer bus.Close()

	events, cancel := bus.Subscribe(nil)
	defer cancel()

	bus.Publish(context.Background(), core.AssistantTextStreamed{Text: "kept"})
	bus.Publish(context.Background(), core.AssistantTextStreamed{Text: "dropped"})

	kept := <-events
	if streamed := kept.(core.AssistantTextStreamed); streamed.Text != "kept" {
		t.Fatalf("got %q, want the first event to survive", streamed.Text)
	}
	select {
	case extra := <-events:
		t.Fatalf("buffer held %v, want the second event dropped", extra)
	default:
	}
	if !strings.Contains(logged.String(), "event dropped") {
		t.Fatalf("got log %q, want a dropped-event warning", logged.String())
	}
}

func TestBusUnsubscribeClosesChannelAndStopsDelivery(t *testing.T) {
	bus := core.BuildEventBus(log.BuildNopLogger(), 4)
	defer bus.Close()

	events, cancel := bus.Subscribe(nil)
	cancel()

	if _, isSubscriptionChannelOpen := <-events; isSubscriptionChannelOpen {
		t.Fatal("got an open channel, want unsubscribe to close it")
	}

	bus.Publish(context.Background(), core.TurnCompleted{Reason: core.StopEndTurn})
}

func TestBusCloseEndsSubscriptionsAndSilencesPublish(t *testing.T) {
	bus := core.BuildEventBus(log.BuildNopLogger(), 4)
	events, cancel := bus.Subscribe(nil)
	defer cancel()

	bus.Close()
	if _, isSubscriptionChannelOpen := <-events; isSubscriptionChannelOpen {
		t.Fatal("got an open channel, want Close to end the subscription")
	}

	bus.Publish(context.Background(), core.TurnCompleted{Reason: core.StopEndTurn})

	afterClose, cancelAfterClose := bus.Subscribe(nil)
	defer cancelAfterClose()
	if _, isSubscriptionChannelOpen := <-afterClose; isSubscriptionChannelOpen {
		t.Fatal("got an open channel, want subscribing after Close to yield a closed one")
	}
}
