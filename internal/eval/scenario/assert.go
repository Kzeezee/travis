package scenario

import (
	"fmt"
	"strings"
)

// Result is what one scenario's run actually produced, collected by the runner (cmd/travis)
// from the real event bus and workspace. It carries only what assertions can check, not raw
// events or file handles, so CheckAssertions stays pure.
type Result struct {
	// ToolsCalled holds each tool name actually invoked during the run, deduplicated.
	ToolsCalled []string
	// Answer is the assistant's streamed text, concatenated across every turn.
	Answer string
	// Files holds the content of exactly the paths named in the scenario's AssertFiles.
	Files map[string]string
}

// CheckAssertions reports the first assertion in sc that result does not satisfy, or nil if
// every one holds.
func CheckAssertions(sc Scenario, result Result) error {
	called := make(map[string]bool, len(result.ToolsCalled))
	for _, name := range result.ToolsCalled {
		called[name] = true
	}

	for _, want := range sc.AssertToolsCalled {
		if !called[want] {
			return fmt.Errorf("%q: tool %q was never called (called: %v)", sc.Name, want, result.ToolsCalled)
		}
	}
	for _, forbidden := range sc.AssertToolsNotCalled {
		if called[forbidden] {
			return fmt.Errorf("%q: tool %q was called, want it untouched", sc.Name, forbidden)
		}
	}
	for _, want := range sc.AssertAnswerContains {
		if !strings.Contains(result.Answer, want) {
			return fmt.Errorf("%q: answer %q lacks %q", sc.Name, result.Answer, want)
		}
	}
	for _, assertFile := range sc.AssertFiles {
		if !strings.Contains(result.Files[assertFile.Path], assertFile.Contains) {
			return fmt.Errorf("%q: file %q lacks %q (content: %q)", sc.Name, assertFile.Path, assertFile.Contains, result.Files[assertFile.Path])
		}
	}
	return nil
}
