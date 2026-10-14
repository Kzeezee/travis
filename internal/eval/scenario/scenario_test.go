package scenario_test

import (
	"strings"
	"testing"

	"github.com/Kzeezee/travis/internal/eval/scenario"
)

func mustDecode(t *testing.T, body string) (scenario.Suite, error) {
	t.Helper()
	return scenario.DecodeSuite(strings.NewReader(body))
}

func TestDecodeSuiteAcceptsACassetteScenario(t *testing.T) {
	suite, err := mustDecode(t, `
[[scenario]]
name = "ok"
fixture = "notes"
cassette = "notes-task"
turns = ["hi"]
`)
	if err != nil {
		t.Fatal(err)
	}
	if len(suite.Scenarios) != 1 || suite.Scenarios[0].Name != "ok" {
		t.Fatalf("got %+v", suite)
	}
}

func TestDecodeSuiteAcceptsAScriptedScenario(t *testing.T) {
	_, err := mustDecode(t, `
[[scenario]]
name = "ok"
fixture = "notes"
turns = ["hi"]

[[scenario.step]]
tool = "grep"
arguments = "{}"

[[scenario.step]]
final_text = "done"
`)
	if err != nil {
		t.Fatal(err)
	}
}

func TestDecodeSuiteRejectsAMissingName(t *testing.T) {
	_, err := mustDecode(t, `
[[scenario]]
fixture = "notes"
cassette = "x"
turns = ["hi"]
`)
	if err == nil {
		t.Fatal("expected an error for a missing name")
	}
}

func TestDecodeSuiteRejectsAMissingFixture(t *testing.T) {
	_, err := mustDecode(t, `
[[scenario]]
name = "ok"
cassette = "x"
turns = ["hi"]
`)
	if err == nil {
		t.Fatal("expected an error for a missing fixture")
	}
}

func TestDecodeSuiteRejectsMissingTurns(t *testing.T) {
	_, err := mustDecode(t, `
[[scenario]]
name = "ok"
fixture = "notes"
cassette = "x"
`)
	if err == nil {
		t.Fatal("expected an error for missing turns")
	}
}

func TestDecodeSuiteRejectsNeitherCassetteNorSteps(t *testing.T) {
	_, err := mustDecode(t, `
[[scenario]]
name = "ok"
fixture = "notes"
turns = ["hi"]
`)
	if err == nil {
		t.Fatal("expected an error when neither cassette nor steps are set")
	}
}

func TestDecodeSuiteRejectsBothCassetteAndSteps(t *testing.T) {
	_, err := mustDecode(t, `
[[scenario]]
name = "ok"
fixture = "notes"
cassette = "x"
turns = ["hi"]

[[scenario.step]]
final_text = "done"
`)
	if err == nil {
		t.Fatal("expected an error when both cassette and steps are set")
	}
}

func TestDecodeSuiteRejectsAStepWithBothToolAndFinalText(t *testing.T) {
	_, err := mustDecode(t, `
[[scenario]]
name = "ok"
fixture = "notes"
turns = ["hi"]

[[scenario.step]]
tool = "grep"
final_text = "done"
`)
	if err == nil {
		t.Fatal("expected an error for a step with both tool and final_text")
	}
}

func TestDecodeSuiteRejectsAStepWithNeitherToolNorFinalText(t *testing.T) {
	_, err := mustDecode(t, `
[[scenario]]
name = "ok"
fixture = "notes"
turns = ["hi"]

[[scenario.step]]
arguments = "{}"
`)
	if err == nil {
		t.Fatal("expected an error for a step with neither tool nor final_text")
	}
}

func buildScenario() scenario.Scenario {
	return scenario.Scenario{
		Name:                 "sample",
		AssertToolsCalled:    []string{"grep"},
		AssertToolsNotCalled: []string{"fs_write"},
		AssertAnswerContains: []string{"October"},
		AssertFiles:          []scenario.AssertFile{{Path: "summary.md", Contains: "10:15"}},
	}
}

func TestCheckAssertionsPassesWhenEverythingHolds(t *testing.T) {
	result := scenario.Result{
		ToolsCalled: []string{"grep", "fs_read"},
		Answer:      "It is on 13 October 2026.",
		Files:       map[string]string{"summary.md": "Moved to 10:15.\n"},
	}
	if err := scenario.CheckAssertions(buildScenario(), result); err != nil {
		t.Fatalf("got %v, want nil", err)
	}
}

func TestCheckAssertionsFailsOnAMissingToolCall(t *testing.T) {
	result := scenario.Result{Answer: "October", Files: map[string]string{"summary.md": "10:15"}}
	if err := scenario.CheckAssertions(buildScenario(), result); err == nil {
		t.Fatal("expected an error: grep was never called")
	}
}

func TestCheckAssertionsFailsOnAForbiddenToolCall(t *testing.T) {
	result := scenario.Result{
		ToolsCalled: []string{"grep", "fs_write"},
		Answer:      "October",
		Files:       map[string]string{"summary.md": "10:15"},
	}
	if err := scenario.CheckAssertions(buildScenario(), result); err == nil {
		t.Fatal("expected an error: fs_write was called")
	}
}

func TestCheckAssertionsFailsOnAMissingAnswerSubstring(t *testing.T) {
	result := scenario.Result{
		ToolsCalled: []string{"grep"},
		Answer:      "no date here",
		Files:       map[string]string{"summary.md": "10:15"},
	}
	if err := scenario.CheckAssertions(buildScenario(), result); err == nil {
		t.Fatal("expected an error: answer lacks October")
	}
}

func TestCheckAssertionsFailsOnWrongOrMissingFileContent(t *testing.T) {
	result := scenario.Result{ToolsCalled: []string{"grep"}, Answer: "October", Files: map[string]string{}}
	if err := scenario.CheckAssertions(buildScenario(), result); err == nil {
		t.Fatal("expected an error: summary.md is missing")
	}
}
