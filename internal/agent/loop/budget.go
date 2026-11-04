package loop

import (
	"context"

	"github.com/Kzeezee/travis/internal/core"
)

// SpendLedger is the money spent today across every session; nil means no daily tracking.
type SpendLedger interface {
	RecordSpend(ctx context.Context, usd float64) error
	ReportSpentToday(ctx context.Context) (float64, error)
}

// Pricing is what the active model costs; both zero means unpriced, so no ceiling can apply.
type Pricing struct {
	InputUSDPerMTok  float64
	OutputUSDPerMTok float64
}

func (p Pricing) isPriced() bool {
	return p.InputUSDPerMTok > 0 || p.OutputUSDPerMTok > 0
}

func (p Pricing) calculateCostUSD(usage core.Usage) float64 {
	return float64(usage.InputTokens)*p.InputUSDPerMTok/1e6 + float64(usage.OutputTokens)*p.OutputUSDPerMTok/1e6
}

// Budget caps a turn and a day; a zero ceiling is off.
type Budget struct {
	MaxTurnUSD  float64
	MaxDailyUSD float64
	Pricing     Pricing
}

// isBudgetExhausted is checked between steps so a stop never leaves a tool call unanswered.
// A ledger that cannot be read stops the turn: a ceiling that fails open is not a ceiling.
func (c *AssistantConversation) isBudgetExhausted(ctx context.Context) bool {
	if !c.budget.Pricing.isPriced() {
		return false
	}
	if c.budget.MaxTurnUSD > 0 && c.turnCostUSD >= c.budget.MaxTurnUSD {
		return true
	}
	if c.budget.MaxDailyUSD > 0 && c.spend != nil {
		if err := c.refreshSpentToday(ctx); err != nil {
			return true
		}
		return c.spentTodayUSD >= c.budget.MaxDailyUSD
	}
	return false
}

// chargeStep counts one step's cost against the turn and the day. It is recorded per step so a
// crash mid-turn still counts; a ledger that refuses the write does not end the turn.
func (c *AssistantConversation) chargeStep(ctx context.Context, usage core.Usage) {
	cost := c.budget.Pricing.calculateCostUSD(usage)
	if cost <= 0 {
		return
	}
	c.turnCostUSD += cost
	if c.spend != nil {
		_ = c.spend.RecordSpend(context.WithoutCancel(ctx), cost)
	}
}

func (c *AssistantConversation) refreshSpentToday(ctx context.Context) error {
	if c.spend == nil {
		return nil
	}
	spent, err := c.spend.ReportSpentToday(ctx)
	if err != nil {
		return err
	}
	c.spentTodayUSD = spent
	return nil
}

// buildSpendingLimitMessage stands in for the reply a turn stopped before its first step never made.
func buildSpendingLimitMessage() core.Message {
	return core.Message{Role: core.RoleAssistant, Parts: []core.Part{core.TextPart{Text: "[travis] This turn stopped: the spending limit was reached."}}}
}
