package broker

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/Kzeezee/travis/internal/core"
	"github.com/Kzeezee/travis/internal/security/policy"
)

func TestRevokingADurableRuleUpdatesTheActiveApprovalLedger(t *testing.T) {
	root := t.TempDir()
	capability := core.Capability{Kind: core.CapabilityFSWrite, Scope: filepath.Join(root, "notes.md")}
	local := BuildLocalBroker(root, core.SystemClock{})
	if err := local.AddDecisionRule(context.Background(), core.DecisionRule{
		Kind: capability.Kind, Scope: capability.Scope, Decision: core.DecisionAllow, Source: "test",
	}); err != nil {
		t.Fatal(err)
	}
	enforced := BuildPolicyEnforcedBroker(root, policy.New(nil, nil, nil, false, ""), buildDiscardingRecords(), nil, "", core.SystemClock{})
	if !enforced.approvals.alwaysAllowed[capability] {
		t.Fatal("durable rule was not loaded into the active ledger")
	}
	snapshot, err := enforced.ReportSecuritySnapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if err := enforced.RevokeDecisionRule(context.Background(), snapshot.Rules[0].ID); err != nil {
		t.Fatal(err)
	}
	if enforced.approvals.alwaysAllowed[capability] {
		t.Fatal("revoked rule remains in the active ledger")
	}
}

func TestSecuritySnapshotIncludesRecentPolicyDenials(t *testing.T) {
	root := t.TempDir()
	enforced := BuildPolicyEnforcedBroker(root, policy.New(nil, nil, nil, false, ""), buildDiscardingRecords(), nil, "", core.SystemClock{})
	if _, err := enforced.DescribePath(context.Background(), filepath.Join(root, "denied.txt")); !errors.Is(err, core.ErrDenied) {
		t.Fatalf("DescribePath = %v, want denied", err)
	}
	snapshot, err := enforced.ReportSecuritySnapshot(context.Background())
	if err != nil || len(snapshot.RecentDenials) != 1 {
		t.Fatalf("snapshot = %+v, %v; want one recent denial", snapshot, err)
	}
}
