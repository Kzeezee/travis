package sandbox

import (
	"strings"
	"testing"
)

// On Landlock ABI 1 (Linux 5.13 to 5.18) a refer rule makes go-landlock's best effort drop Landlock entirely,
// so a write rule may ask for refer only where the kernel can grant it.
func TestWriteRuleAsksForReferOnlyWhereTheKernelCanGrantIt(t *testing.T) {
	dir := t.TempDir()
	if rule := buildPathRule(dir, true, false).String(); strings.Contains(rule, "refer") {
		t.Fatalf("ABI 1 write rule = %s, want no refer", rule)
	}
	if rule := buildPathRule(dir, true, true).String(); !strings.Contains(rule, "refer") {
		t.Fatalf("ABI 2+ write rule = %s, want refer for renames between folders", rule)
	}
}
