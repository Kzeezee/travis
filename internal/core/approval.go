package core

import "context"

// Operation is a pending action that policy evaluated to Ask (§6.4). Explanation is the
// evaluation trace, shown verbatim to the human who must decide.
type Operation struct {
	Kind        CapabilityKind
	Target      string
	Explanation string
}

// ApprovalDecision is the human's answer to a pending Operation.
type ApprovalDecision int

const (
	ApprovalDenied ApprovalDecision = iota
	ApprovalOnce
	ApprovalAlways
)

func (d ApprovalDecision) String() string {
	switch d {
	case ApprovalDenied:
		return "denied"
	case ApprovalOnce:
		return "once"
	case ApprovalAlways:
		return "always"
	default:
		return "denied"
	}
}

// ApprovalRequest is delivered to a frontend, like events are, but it is an operation,
// not an event (AGENTS.md: events are facts that already happened; approvals are
// required work with a return value). Respond must be called exactly once, and only ever
// from a real keypress (D-10) — never guessed from model output.
type ApprovalRequest struct {
	Operation Operation
	Respond   func(ApprovalDecision)
}

// Approver requests permission for an Operation and blocks until a human answers. It is
// the human-in-the-loop primitive called wherever a policy decision is Ask (§6.4).
type Approver interface {
	RequestApproval(ctx context.Context, op Operation) (ApprovalDecision, error)
}
