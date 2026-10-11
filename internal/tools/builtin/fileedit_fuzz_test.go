package builtin

import (
	"strings"
	"testing"
)

func FuzzFileEditMatching(f *testing.F) {
	f.Add("alpha\nbeta\n", "beta", "BETA")
	f.Add("aaa", "a", "b")
	f.Add("abc", "x", "y")
	f.Fuzz(func(t *testing.T, file, old, replacement string) {
		if old == "" {
			t.Skip()
		}
		got, occurrences := spliceSingleOccurrence(file, old, replacement)
		if occurrences != 1 {
			if got != file {
				t.Fatalf("count %d changed the file: %q -> %q", occurrences, file, got)
			}
			return
		}
		if !strings.Contains(got, replacement) || len(got) != len(file)-len(old)+len(replacement) {
			t.Fatalf("splice(%q, %q, %q) = %q", file, old, replacement, got)
		}
	})
}
