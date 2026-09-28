package id_test

import (
	"testing"
	"time"

	"github.com/Kzeezee/travis/internal/core"
	"github.com/Kzeezee/travis/internal/id"
)

func TestGenerateIDProducesDistinctNonEmptyIDs(t *testing.T) {
	generator := id.BuildULIDGenerator(core.FixedClock{Time: time.Unix(1_700_000_000, 0)})

	first := generator.GenerateID()
	second := generator.GenerateID()

	if first == "" || second == "" {
		t.Fatalf("empty ID: %q, %q", first, second)
	}
	if first == second {
		t.Fatalf("IDs collided: %q", first)
	}
}

func TestGenerateIDSortsByClockTime(t *testing.T) {
	earlier := time.Unix(1_700_000_000, 0)
	later := earlier.Add(time.Millisecond)

	earlierID := id.BuildULIDGenerator(core.FixedClock{Time: earlier}).GenerateID()
	laterID := id.BuildULIDGenerator(core.FixedClock{Time: later}).GenerateID()

	if earlierID >= laterID {
		t.Fatalf("expected %q < %q", earlierID, laterID)
	}
}
