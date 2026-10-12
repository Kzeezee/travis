package core

import "time"

// DecisionOutcome is the only thing a durable rule may grant: it narrows a future Ask
// verdict to Allow. There is deliberately no "deny" outcome yet — out of scope for this
// ticket.
type DecisionOutcome string

const DecisionAllow DecisionOutcome = "allow"

// DecisionRule is a durable, user-approved override (D-30, §6.4: "rules narrow, never
// widen"). It is never constructed from a tool call or a file write — only from a real
// keypress in the approval modal, via Broker.AddDecisionRule.
type DecisionRule struct {
	Kind     CapabilityKind
	Scope    string // the exact already-approved target, never a generalisation
	Decision DecisionOutcome
	Source   string // free-form audit text, e.g. who/what approved this and when
	AddedAt  time.Time
}
