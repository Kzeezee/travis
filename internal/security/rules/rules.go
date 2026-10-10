// Package rules turns a just-approved Operation into a DecisionRule that is provably no
// wider than what the policy already grants (D-30, §6.4). It does no IO and never imports
// broker, filesystem, provider, UI, or MCP.
package rules

import (
	"fmt"

	"github.com/Kzeezee/travis/internal/core"
	"github.com/Kzeezee/travis/internal/security/capabilities"
)

// ValidateNarrowing reports whether op may become a durable rule: its Kind must not be
// exec (D-27, unrelaxable) and its concrete Target must already be covered by an existing
// grant. The returned rule's Scope is exactly op.Target, never a generalisation, so
// narrowing is true by construction rather than by glob-subset reasoning. Generalising to
// directory-level globs is a deliberate, documented follow-up, not part of this ticket.
func ValidateNarrowing(grants capabilities.Set, op core.Operation, isCaseInsensitive bool, source string) (core.DecisionRule, error) {
	if op.Kind == core.CapabilityExec {
		return core.DecisionRule{}, fmt.Errorf("exec can never become a decision rule (D-27): %w", core.ErrDenied)
	}
	if _, isGranted := grants.SelectMatching(op.Kind, op.Target, isCaseInsensitive); !isGranted {
		return core.DecisionRule{}, fmt.Errorf("%s %q is not within any existing grant: %w", op.Kind, op.Target, core.ErrDenied)
	}
	return core.DecisionRule{Kind: op.Kind, Scope: op.Target, Decision: core.DecisionAllow, Source: source}, nil
}
