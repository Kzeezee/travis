package core

import "testing"

var _ IDGenerator = (*SequentialIDGenerator)(nil)

func TestSequentialIDGeneratorCountsUpFromOne(t *testing.T) {
	generator := &SequentialIDGenerator{Prefix: "evt-"}

	for _, want := range []ID{"evt-1", "evt-2", "evt-3"} {
		if got := generator.GenerateID(); got != want {
			t.Fatalf("GenerateID() = %q, want %q", got, want)
		}
	}
}
