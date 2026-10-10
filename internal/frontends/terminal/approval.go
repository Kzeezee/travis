package terminal

import (
	"context"

	"github.com/Kzeezee/travis/internal/core"
)

// BuildApprover returns a core.Approver that delivers requests on requests and blocks
// until the running chat's modal answers them. cmd/travis is the only caller: it owns
// both ends of the channel this wraps.
func BuildApprover(requests chan<- core.ApprovalRequest) core.Approver {
	return channelApprover{requests: requests}
}

type channelApprover struct {
	requests chan<- core.ApprovalRequest
}

func (a channelApprover) RequestApproval(ctx context.Context, op core.Operation) (core.ApprovalDecision, error) {
	answers := make(chan core.ApprovalDecision, 1)
	request := core.ApprovalRequest{Operation: op, Respond: func(d core.ApprovalDecision) { answers <- d }}

	select {
	case a.requests <- request:
	case <-ctx.Done():
		return core.ApprovalDenied, ctx.Err()
	}
	select {
	case decision := <-answers:
		return decision, nil
	case <-ctx.Done():
		return core.ApprovalDenied, ctx.Err()
	}
}
