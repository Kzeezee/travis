package loop_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/Kzeezee/travis/internal/agent/loop"
	"github.com/Kzeezee/travis/internal/core"
)

var errLedgerBroken = errors.New("ledger broken")

// fakeLedger holds the day's total in memory; recordErr and readErr make either side fail.
type fakeLedger struct {
	spent     float64
	recorded  []float64
	recordErr error
	readErr   error
}

func (l *fakeLedger) RecordSpend(_ context.Context, usd float64) error {
	if l.recordErr != nil {
		return l.recordErr
	}
	l.recorded = append(l.recorded, usd)
	l.spent += usd
	return nil
}

func (l *fakeLedger) ReportSpentToday(context.Context) (float64, error) {
	return l.spent, l.readErr
}

// onePerThousandInputTokens makes 1000 input tokens cost $1, so a step's usage reads as its price.
var onePerThousandInputTokens = loop.Pricing{InputUSDPerMTok: 1000}

func buildBudgetedConversation(provider core.Provider, sink core.EventSink, ledger loop.SpendLedger, budget loop.Budget) *loop.AssistantConversation {
	deps := loop.Dependencies{
		Provider: provider,
		Tools:    &fakeToolLookup{tools: []core.Tool{&echoTool{}}},
		Events:   sink,
		Clock:    core.FixedClock{Time: time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)},
		IDs:      &core.SequentialIDGenerator{Prefix: "id-"},
		Budget:   budget,
		Spend:    ledger,
	}
	return loop.BuildAssistantConversation(deps, "test-model", testMaxSteps)
}

func buildPricedToolStep(inputTokens int) *fakeStream {
	return buildTextStream(core.Chunk{
		ToolCalls: []core.ToolCall{{ID: "call", Name: "echo", Arguments: json.RawMessage(`{"text":"x"}`)}},
		Usage:     &core.Usage{InputTokens: inputTokens},
		Finish:    core.StopToolCalls,
	})
}

func TestTurnCeilingStopsAfterTheStepThatCrossesItAndKeepsHistoryValid(t *testing.T) {
	provider := &fakeProvider{streams: []*fakeStream{
		buildPricedToolStep(400), buildPricedToolStep(600), buildPricedToolStep(100),
		buildTextStream(core.Chunk{Text: "next turn"}),
	}}
	sink := &recordingSink{}
	ledger := &fakeLedger{}
	conversation := buildBudgetedConversation(provider, sink, ledger, loop.Budget{MaxTurnUSD: 1, Pricing: onePerThousandInputTokens})

	if err := conversation.RunAssistantTurn(context.Background(), "go"); err != nil {
		t.Fatalf("RunAssistantTurn returned %v, want nil", err)
	}

	if len(provider.requests) != 2 {
		t.Fatalf("got %d provider calls, want 2: the second step crossed $1 so no third may start", len(provider.requests))
	}
	completed := assertTurnCompleted(t, sink, core.StopBudget)
	if completed.CostUSD != 1 {
		t.Fatalf("got turn cost %v, want 1", completed.CostUSD)
	}
	if len(ledger.recorded) != 2 {
		t.Fatalf("got %d ledger records, want one per step", len(ledger.recorded))
	}

	if err := conversation.RunAssistantTurn(context.Background(), "again"); err != nil {
		t.Fatalf("second RunAssistantTurn returned %v, want nil", err)
	}
	if next := provider.requests[2].Messages; next[len(next)-1].Role != core.RoleUser || next[len(next)-2].Role != core.RoleTool {
		t.Fatalf("got roles ending %q after %q, want the stopped turn's tool results answered before the new user message", next[len(next)-1].Role, next[len(next)-2].Role)
	}
}

func TestDailyCeilingStopsBeforeTheFirstStepAndAnswersTheUser(t *testing.T) {
	provider := &fakeProvider{streams: []*fakeStream{buildTextStream(core.Chunk{Text: "ok"})}}
	sink := &recordingSink{}
	ledger := &fakeLedger{spent: 5}
	conversation := buildBudgetedConversation(provider, sink, ledger, loop.Budget{MaxDailyUSD: 5, Pricing: onePerThousandInputTokens})

	if err := conversation.RunAssistantTurn(context.Background(), "first"); err != nil {
		t.Fatalf("RunAssistantTurn returned %v, want nil", err)
	}

	if len(provider.requests) != 0 {
		t.Fatalf("got %d provider calls, want none once the day's limit is reached", len(provider.requests))
	}
	if completed := assertTurnCompleted(t, sink, core.StopBudget); completed.SpentTodayUSD != 5 {
		t.Fatalf("got spent today %v, want 5", completed.SpentTodayUSD)
	}

	ledger.spent = 0
	if err := conversation.RunAssistantTurn(context.Background(), "second"); err != nil {
		t.Fatalf("second RunAssistantTurn returned %v, want nil", err)
	}
	assertStubFollowsUser(t, provider.requests[0].Messages, "[travis] This turn stopped: the spending limit was reached.")
}

func TestDailyCeilingCountsSpendFromOtherSessions(t *testing.T) {
	provider := &fakeProvider{streams: []*fakeStream{buildPricedToolStep(1500), buildPricedToolStep(1500)}}
	sink := &recordingSink{}
	ledger := &fakeLedger{spent: 1.5}
	conversation := buildBudgetedConversation(provider, sink, ledger, loop.Budget{MaxDailyUSD: 3, Pricing: onePerThousandInputTokens})

	if err := conversation.RunAssistantTurn(context.Background(), "go"); err != nil {
		t.Fatalf("RunAssistantTurn returned %v, want nil", err)
	}

	if len(provider.requests) != 1 {
		t.Fatalf("got %d provider calls, want 1: 1.5 from elsewhere plus the first step reaches $3", len(provider.requests))
	}
	assertTurnCompleted(t, sink, core.StopBudget)
}

func TestUnpricedProfileIgnoresCeilingsAndRecordsNothing(t *testing.T) {
	provider := &fakeProvider{streams: []*fakeStream{buildPricedToolStep(900_000), buildTextStream(core.Chunk{Text: "done", Finish: core.StopEndTurn})}}
	sink := &recordingSink{}
	ledger := &fakeLedger{spent: 100}
	conversation := buildBudgetedConversation(provider, sink, ledger, loop.Budget{MaxTurnUSD: 0.01, MaxDailyUSD: 1})

	if err := conversation.RunAssistantTurn(context.Background(), "go"); err != nil {
		t.Fatalf("RunAssistantTurn returned %v, want nil", err)
	}

	if completed := assertTurnCompleted(t, sink, core.StopEndTurn); completed.CostUSD != 0 {
		t.Fatalf("got turn cost %v, want 0 for an unpriced model", completed.CostUSD)
	}
	if len(ledger.recorded) != 0 {
		t.Fatalf("got ledger records %v, want none", ledger.recorded)
	}
}

func TestUnreadableLedgerStopsTheTurnWhenADailyCeilingIsSet(t *testing.T) {
	provider := &fakeProvider{}
	sink := &recordingSink{}
	ledger := &fakeLedger{readErr: errLedgerBroken}
	conversation := buildBudgetedConversation(provider, sink, ledger, loop.Budget{MaxDailyUSD: 5, Pricing: onePerThousandInputTokens})

	if err := conversation.RunAssistantTurn(context.Background(), "go"); err != nil {
		t.Fatalf("RunAssistantTurn returned %v, want nil", err)
	}

	if len(provider.requests) != 0 {
		t.Fatalf("got %d provider calls, want none when the day's spend cannot be read", len(provider.requests))
	}
	assertTurnCompleted(t, sink, core.StopBudget)
}

func TestUnwritableLedgerDoesNotEndTheTurn(t *testing.T) {
	provider := &fakeProvider{streams: []*fakeStream{buildTextStream(core.Chunk{Text: "hi", Usage: &core.Usage{InputTokens: 500}, Finish: core.StopEndTurn})}}
	sink := &recordingSink{}
	ledger := &fakeLedger{recordErr: errLedgerBroken}
	conversation := buildBudgetedConversation(provider, sink, ledger, loop.Budget{MaxTurnUSD: 5, Pricing: onePerThousandInputTokens})

	if err := conversation.RunAssistantTurn(context.Background(), "go"); err != nil {
		t.Fatalf("RunAssistantTurn returned %v, want nil", err)
	}

	if completed := assertTurnCompleted(t, sink, core.StopEndTurn); completed.CostUSD != 0.5 {
		t.Fatalf("got turn cost %v, want 0.5 even though the ledger refused the write", completed.CostUSD)
	}
}

func TestSelectProviderPricesLaterTurns(t *testing.T) {
	cheap := &fakeProvider{streams: []*fakeStream{buildTextStream(core.Chunk{Text: "hi", Usage: &core.Usage{InputTokens: 1000}, Finish: core.StopEndTurn})}}
	sink := &recordingSink{}
	conversation := buildBudgetedConversation(&fakeProvider{}, sink, &fakeLedger{}, loop.Budget{MaxTurnUSD: 5})

	if err := conversation.SelectProvider(cheap, "priced", onePerThousandInputTokens, 0); err != nil {
		t.Fatalf("SelectProvider returned %v, want nil", err)
	}
	if err := conversation.RunAssistantTurn(context.Background(), "go"); err != nil {
		t.Fatalf("RunAssistantTurn returned %v, want nil", err)
	}

	if completed := assertTurnCompleted(t, sink, core.StopEndTurn); completed.CostUSD != 1 {
		t.Fatalf("got turn cost %v, want 1 at the new provider's price", completed.CostUSD)
	}
}
