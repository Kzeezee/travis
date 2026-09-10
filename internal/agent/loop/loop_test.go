package loop_test

import (
	"context"
	"errors"
	"io"
	"testing"
	"time"

	"github.com/Kzeezee/travis/internal/agent/loop"
	"github.com/Kzeezee/travis/internal/core"
)

var errProviderUnavailable = errors.New("provider unavailable")

type fakeStream struct {
	chunks []core.Chunk
	failAt int // index at which Next returns errProviderUnavailable; negative disables
	next   int
	closed bool
}

func (s *fakeStream) Next() (core.Chunk, error) {
	if s.failAt >= 0 && s.next == s.failAt {
		return core.Chunk{}, errProviderUnavailable
	}
	if s.next >= len(s.chunks) {
		return core.Chunk{}, io.EOF
	}
	chunk := s.chunks[s.next]
	s.next++
	return chunk, nil
}

func (s *fakeStream) Close() error {
	s.closed = true
	return nil
}

type fakeProvider struct {
	stream   *fakeStream
	chatErr  error
	requests []core.ProviderRequest
}

func (p *fakeProvider) Chat(_ context.Context, request core.ProviderRequest) (core.Stream, error) {
	p.requests = append(p.requests, request)
	if p.chatErr != nil {
		return nil, p.chatErr
	}
	return p.stream, nil
}

type recordingSink struct {
	events []core.Event
}

func (s *recordingSink) Publish(_ context.Context, event core.Event) {
	s.events = append(s.events, event)
}

func (s *recordingSink) types() []core.EventType {
	types := make([]core.EventType, 0, len(s.events))
	for _, event := range s.events {
		types = append(types, event.ReportEventType())
	}
	return types
}

func buildRunner(t *testing.T, provider core.Provider, sink core.EventSink) *loop.Runner {
	t.Helper()
	clock := core.FixedClock{Time: time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)}
	return loop.BuildRunner(provider, sink, clock, "test-model")
}

func TestRunStreamsTextAndCompletesTheTurn(t *testing.T) {
	stream := &fakeStream{failAt: -1, chunks: []core.Chunk{
		{Text: "hel"},
		{Text: "lo"},
		{Usage: &core.Usage{InputTokens: 7, OutputTokens: 2}, Finish: core.StopEndTurn},
	}}
	provider := &fakeProvider{stream: stream}
	sink := &recordingSink{}

	if err := buildRunner(t, provider, sink).Run(context.Background(), "hi"); err != nil {
		t.Fatalf("Run returned %v, want nil", err)
	}

	want := []core.EventType{
		core.EventUserMessageSubmitted,
		core.EventAssistantTextStreamed,
		core.EventAssistantTextStreamed,
		core.EventTurnCompleted,
	}
	got := sink.types()
	if len(got) != len(want) {
		t.Fatalf("got events %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got events %v, want %v", got, want)
		}
	}

	completed, ok := sink.events[len(sink.events)-1].(core.TurnCompleted)
	if !ok {
		t.Fatalf("got %T as the final event, want core.TurnCompleted", sink.events[len(sink.events)-1])
	}
	if completed.Reason != core.StopEndTurn {
		t.Fatalf("got reason %q, want %q", completed.Reason, core.StopEndTurn)
	}
	if completed.Usage.OutputTokens != 2 {
		t.Fatalf("got output tokens %d, want 2", completed.Usage.OutputTokens)
	}
	if !stream.closed {
		t.Fatal("stream was left open")
	}
}

func TestRunSendsPriorTurnsAsContext(t *testing.T) {
	provider := &fakeProvider{stream: &fakeStream{failAt: -1, chunks: []core.Chunk{{Text: "first reply"}}}}
	runner := buildRunner(t, provider, &recordingSink{})

	if err := runner.Run(context.Background(), "first"); err != nil {
		t.Fatalf("first Run returned %v, want nil", err)
	}
	provider.stream = &fakeStream{failAt: -1, chunks: []core.Chunk{{Text: "second reply"}}}
	if err := runner.Run(context.Background(), "second"); err != nil {
		t.Fatalf("second Run returned %v, want nil", err)
	}

	if len(provider.requests) != 2 {
		t.Fatalf("got %d provider requests, want 2", len(provider.requests))
	}
	second := provider.requests[1].Messages
	if len(second) != 3 {
		t.Fatalf("got %d messages on the second turn, want user, assistant, user", len(second))
	}
	if second[0].Role != core.RoleUser || second[1].Role != core.RoleAssistant || second[2].Role != core.RoleUser {
		t.Fatalf("got roles %q/%q/%q, want user/assistant/user", second[0].Role, second[1].Role, second[2].Role)
	}
	replyPart, ok := second[1].Parts[0].(core.TextPart)
	if !ok || replyPart.Text != "first reply" {
		t.Fatalf("got recorded reply %v, want the streamed text", second[1].Parts[0])
	}
}

func TestRunReportsAFailedProviderCall(t *testing.T) {
	provider := &fakeProvider{chatErr: errProviderUnavailable}
	sink := &recordingSink{}

	err := buildRunner(t, provider, sink).Run(context.Background(), "hi")

	if !errors.Is(err, errProviderUnavailable) {
		t.Fatalf("got %v, want errProviderUnavailable", err)
	}
	assertTurnEndedWithError(t, sink)
}

func TestRunReportsAFailureMidStream(t *testing.T) {
	provider := &fakeProvider{stream: &fakeStream{failAt: 1, chunks: []core.Chunk{{Text: "partial"}}}}
	sink := &recordingSink{}

	err := buildRunner(t, provider, sink).Run(context.Background(), "hi")

	if !errors.Is(err, errProviderUnavailable) {
		t.Fatalf("got %v, want errProviderUnavailable", err)
	}
	assertTurnEndedWithError(t, sink)
}

func assertTurnEndedWithError(t *testing.T, sink *recordingSink) {
	t.Helper()
	last := sink.events[len(sink.events)-1]
	completed, ok := last.(core.TurnCompleted)
	if !ok {
		t.Fatalf("got %T as the final event, want core.TurnCompleted", last)
	}
	if completed.Reason != core.StopError {
		t.Fatalf("got reason %q, want %q", completed.Reason, core.StopError)
	}
}
