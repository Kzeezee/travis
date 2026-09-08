package core

import (
	"testing"
	"time"
)

func TestFixedClockReturnsConfiguredTime(t *testing.T) {
	want := time.Date(2026, 9, 5, 12, 0, 0, 0, time.UTC)
	clock := FixedClock{Time: want}

	if got := clock.Now(); !got.Equal(want) {
		t.Fatalf("FixedClock.Now() = %v, want %v", got, want)
	}
}
