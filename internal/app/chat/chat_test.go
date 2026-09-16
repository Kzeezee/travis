package chat_test

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/Kzeezee/travis/internal/agent/loop"
	"github.com/Kzeezee/travis/internal/app/chat"
	"github.com/Kzeezee/travis/internal/core"
	"github.com/Kzeezee/travis/internal/frontends/terminal"
	"github.com/Kzeezee/travis/internal/log"
)

var errProviderUnavailable = errors.New("provider unavailable")

type fakeStream struct {
	chunks []core.Chunk
	next   int
}

func (s *fakeStream) Next() (core.Chunk, error) {
	if s.next >= len(s.chunks) {
		return core.Chunk{}, io.EOF
	}
	chunk := s.chunks[s.next]
	s.next++
	return chunk, nil
}

func (s *fakeStream) Close() error {
	return nil
}

type fakeProvider struct {
	streams  []*fakeStream
	chatErrs []error
	requests []core.ProviderRequest
}

func (p *fakeProvider) Chat(_ context.Context, request core.ProviderRequest) (core.Stream, error) {
	p.requests = append(p.requests, request)
	if len(p.chatErrs) > 0 {
		err := p.chatErrs[0]
		p.chatErrs = p.chatErrs[1:]
		if err != nil {
			return nil, err
		}
	}
	if len(p.streams) == 0 {
		return &fakeStream{}, nil
	}
	stream := p.streams[0]
	p.streams = p.streams[1:]
	return stream, nil
}

func buildTestChatSession(t *testing.T, input string, provider *fakeProvider) (*chat.ChatSession, *strings.Builder) {
	t.Helper()

	bus := core.BuildEventBus(log.BuildNopLogger(), 16)
	t.Cleanup(bus.Close)
	events, unsubscribe := bus.Subscribe(nil)
	t.Cleanup(unsubscribe)

	clock := core.FixedClock{Time: time.Date(2026, 9, 14, 12, 0, 0, 0, time.UTC)}
	assistantConversation := loop.BuildAssistantConversation(provider, bus, clock, "test-model")
	var out strings.Builder
	chatTerminal := terminal.BuildTerminal(strings.NewReader(input), &out)
	return chat.BuildChatSession(events, chatTerminal, assistantConversation), &out
}

func TestRunReadsPromptsRunsTurnsAndRendersEvents(t *testing.T) {
	provider := &fakeProvider{streams: []*fakeStream{
		{chunks: []core.Chunk{{Text: "hel"}, {Text: "lo"}, {Finish: core.StopEndTurn}}},
		{chunks: []core.Chunk{{Text: "bye"}}},
	}}
	session, out := buildTestChatSession(t, "first\n\n  second  \n", provider)

	if err := session.Run(context.Background()); err != nil {
		t.Fatalf("Run returned %v, want nil", err)
	}
	if len(provider.requests) != 2 {
		t.Fatalf("got %d provider requests, want 2", len(provider.requests))
	}
	if got := provider.requests[0].Messages[0]; got.Role != core.RoleUser {
		t.Fatalf("got first request role %q, want user", got.Role)
	}
	if second := provider.requests[1].Messages; len(second) != 3 {
		t.Fatalf("got %d messages on second request, want prior user, assistant, user", len(second))
	}
	rendered := out.String()
	if !strings.Contains(rendered, "hello") || !strings.Contains(rendered, "bye") {
		t.Fatalf("got output %q, want both streamed replies", rendered)
	}
}

func TestRunReportsTurnErrorAndKeepsGoing(t *testing.T) {
	provider := &fakeProvider{
		chatErrs: []error{errProviderUnavailable, nil},
		streams:  []*fakeStream{{chunks: []core.Chunk{{Text: "recovered"}}}},
	}
	session, out := buildTestChatSession(t, "one\ntwo\n", provider)

	if err := session.Run(context.Background()); err != nil {
		t.Fatalf("Run returned %v, want nil", err)
	}
	if len(provider.requests) != 2 {
		t.Fatalf("got %d attempts, want session to continue after a failed turn", len(provider.requests))
	}
	rendered := out.String()
	if !strings.Contains(rendered, "error: provider unavailable") || !strings.Contains(rendered, "recovered") {
		t.Fatalf("got output %q, want error then recovered reply", rendered)
	}
}

func TestRunStopsOnQuitWithoutCallingProvider(t *testing.T) {
	provider := &fakeProvider{}
	session, _ := buildTestChatSession(t, "/quit\nignored\n", provider)

	if err := session.Run(context.Background()); err != nil {
		t.Fatalf("Run returned %v, want nil", err)
	}
	if len(provider.requests) != 0 {
		t.Fatalf("got %d provider requests, want none", len(provider.requests))
	}
}
