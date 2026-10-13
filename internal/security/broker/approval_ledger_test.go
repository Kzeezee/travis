package broker

import (
	"testing"

	"github.com/Kzeezee/travis/internal/core"
)

func TestRedTeamGateUntrustedStepCannotReuseAnAlwaysApproval(t *testing.T) {
	capability := core.Capability{Kind: core.CapabilityFSWrite, Scope: "notes.md"}
	ledger := approvalLedger{alwaysAllowed: map[core.Capability]bool{capability: true}, onceAllowed: map[core.Capability]bool{}}
	if !ledger.isAllowed(capability, false) {
		t.Fatal("clean step lost its always approval")
	}
	if ledger.isAllowed(capability, true) {
		t.Fatal("untrusted step reused an always approval")
	}
	ledger.onceAllowed[capability] = true
	if !ledger.isAllowed(capability, true) {
		t.Fatal("untrusted step lost its fresh once approval")
	}
}
