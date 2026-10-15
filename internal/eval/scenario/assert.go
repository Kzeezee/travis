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
	// ToolsOffered holds the tool names in the first provider call's own request — what
	// Registry.Prune actually returned for this session. Empty when the runner has no way
	// to observe it (a cassette replay never exposes the request it would have made).
	ToolsOffered []string
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
	offered := make(map[string]bool, len(result.ToolsOffered))
	for _, name := range result.ToolsOffered {
		offered[name] = true
	}
	for _, want := range sc.AssertToolsOffered {
		if !offered[want] {
			return fmt.Errorf("%q: tool %q was never offered to the model (offered: %v)", sc.Name, want, result.ToolsOffered)
		}
	}
	for _, want := range sc.AssertAnswerContains {
		if !strings.Contains(result.Answer, want) {
			return fmt.Errorf("%q: answer %q lacks %q", sc.Name, result.Answer, want)
		}
	}
	for _, assertFile := range sc.AssertFiles {
		if assertFile.IsAbsent {
			if content, exists := result.Files[assertFile.Path]; exists {
				return fmt.Errorf("%q: file %q exists, want it never written (content: %q)", sc.Name, assertFile.Path, content)
			}
			continue
		}
		if !strings.Contains(result.Files[assertFile.Path], assertFile.Contains) {
			return fmt.Errorf("%q: file %q lacks %q (content: %q)", sc.Name, assertFile.Path, assertFile.Contains, result.Files[assertFile.Path])
		}
	}
	return nil
}
