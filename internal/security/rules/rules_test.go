package rules_test

import (
	"errors"
	"testing"

	"github.com/Kzeezee/travis/internal/core"
	"github.com/Kzeezee/travis/internal/security/capabilities"
	"github.com/Kzeezee/travis/internal/security/rules"
)

func TestValidateNarrowingBuildsARuleScopedToExactlyTheTarget(t *testing.T) {
	grants := capabilities.Set{{Kind: core.CapabilityFSWrite, Scope: "/w/**"}}
	op := core.Operation{Kind: core.CapabilityFSWrite, Target: "/w/notes/today.md"}

	rule, err := rules.ValidateNarrowing(grants, op, false, "user approved")
	if err != nil {
		t.Fatal(err)
	}
	if rule.Kind != op.Kind || rule.Scope != op.Target || rule.Decision != core.DecisionAllow || rule.Source != "user approved" {
		t.Fatalf("ValidateNarrowing = %+v", rule)
	}
}

func TestValidateNarrowingRejectsExecRegardlessOfGrants(t *testing.T) {
	grants := capabilities.Set{{Kind: core.CapabilityExec, Scope: "*"}}
	op := core.Operation{Kind: core.CapabilityExec, Target: "git push"}

	if _, err := rules.ValidateNarrowing(grants, op, false, "user approved"); !errors.Is(err, core.ErrDenied) {
		t.Fatalf("ValidateNarrowing(exec) error = %v, want ErrDenied", err)
	}
}

func TestValidateNarrowingRejectsTargetsNotAlreadyGranted(t *testing.T) {
	grants := capabilities.Set{{Kind: core.CapabilityFSWrite, Scope: "/w/**"}}
	op := core.Operation{Kind: core.CapabilityFSWrite, Target: "/elsewhere/notes.md"}

	if _, err := rules.ValidateNarrowing(grants, op, false, "user approved"); !errors.Is(err, core.ErrDenied) {
		t.Fatalf("ValidateNarrowing(ungranted) error = %v, want ErrDenied", err)
	}
}
