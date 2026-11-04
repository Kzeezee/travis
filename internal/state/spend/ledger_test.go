package spend_test

import (
	"context"
	"math"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Kzeezee/travis/internal/core"
	"github.com/Kzeezee/travis/internal/state/spend"
)

var today = core.FixedClock{Time: time.Date(2026, 11, 4, 9, 0, 0, 0, time.Local)}

func reportSpent(t *testing.T, ledger *spend.Ledger) float64 {
	t.Helper()
	spent, err := ledger.ReportSpentToday(context.Background())
	if err != nil {
		t.Fatalf("ReportSpentToday returned %v", err)
	}
	return spent
}

func recordSpend(t *testing.T, ledger *spend.Ledger, usd float64) {
	t.Helper()
	if err := ledger.RecordSpend(context.Background(), usd); err != nil {
		t.Fatalf("RecordSpend returned %v", err)
	}
}

func TestTwoSessionsOnOneDirectoryBothCount(t *testing.T) {
	dir := t.TempDir()
	first, second := spend.BuildLedger(dir, today), spend.BuildLedger(dir, today)
	recordSpend(t, first, 0.25)
	recordSpend(t, second, 0.5)

	for name, ledger := range map[string]*spend.Ledger{"first": first, "second": second} {
		if got := reportSpent(t, ledger); math.Abs(got-0.75) > 1e-9 {
			t.Errorf("%s session sees %v, want 0.75", name, got)
		}
	}
}

func TestRestartedLedgerSeesTheTotal(t *testing.T) {
	dir := t.TempDir()
	recordSpend(t, spend.BuildLedger(dir, today), 1.5)

	if got := reportSpent(t, spend.BuildLedger(dir, today)); got != 1.5 {
		t.Fatalf("restarted ledger sees %v, want 1.5", got)
	}
}

func TestOtherDaysAreIgnored(t *testing.T) {
	dir := t.TempDir()
	yesterday := core.FixedClock{Time: today.Time.AddDate(0, 0, -1)}
	recordSpend(t, spend.BuildLedger(dir, yesterday), 9)
	recordSpend(t, spend.BuildLedger(dir, today), 0.1)

	if got := reportSpent(t, spend.BuildLedger(dir, today)); got != 0.1 {
		t.Fatalf("today's total is %v, want 0.1", got)
	}
}

func TestEmptyLedgerReportsZero(t *testing.T) {
	if got := reportSpent(t, spend.BuildLedger(filepath.Join(t.TempDir(), "spend"), today)); got != 0 {
		t.Fatalf("empty ledger reports %v, want 0", got)
	}
}

func TestUnparsableLineCountsAsZeroWithoutFailingTheSum(t *testing.T) {
	dir := t.TempDir()
	ledger := spend.BuildLedger(dir, today)
	if err := os.WriteFile(filepath.Join(dir, "2026-11-04.log"), []byte("0.5\n1.2.3\n\n0.25\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	if got := reportSpent(t, ledger); got != 0.75 {
		t.Fatalf("total is %v, want 0.75", got)
	}
}
