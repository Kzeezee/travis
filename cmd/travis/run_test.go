//go:build eval

package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/Kzeezee/travis/internal/core"
)

func TestHeadlessAnswersParseDecisionsInOrder(t *testing.T) {
	path := writeAnswersFile(t, `{"approvals": ["once", "deny"], "questions": ["yes"]}`)

	answers, err := loadScriptedAnswers(path)
	if err != nil {
		t.Fatal(err)
	}
	if want := []core.ApprovalDecision{core.ApprovalOnce, core.ApprovalDenied}; fmt.Sprint(answers.approvals) != fmt.Sprint(want) {
		t.Errorf("approvals = %v, want %v", answers.approvals, want)
	}
	if len(answers.questions) != 1 || answers.questions[0] != "yes" {
		t.Errorf("questions = %q, want [yes]", answers.questions)
	}
}

func TestHeadlessAnswersRejectMalformedFiles(t *testing.T) {
	for name, content := range map[string]string{
		"unknown approval": `{"approvals": ["always"]}`,
		"unknown field":    `{"approvals": [], "aproval": ["once"]}`,
		"trailing data":    `{"approvals": ["once"]} {"approvals": ["deny"]}`,
		"not json":         `approvals: [once]`,
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := loadScriptedAnswers(writeAnswersFile(t, content)); !errors.Is(err, core.ErrInvalid) {
				t.Errorf("err = %v, want core.ErrInvalid", err)
			}
		})
	}
}

func TestHeadlessHumanDeniesOnceApprovalsRunOut(t *testing.T) {
	human := &scriptedHuman{answers: scriptedAnswers{approvals: []core.ApprovalDecision{core.ApprovalOnce}}}

	for _, want := range []core.ApprovalDecision{core.ApprovalOnce, core.ApprovalDenied} {
		if got, _ := human.RequestApproval(t.Context(), core.Operation{}); got != want {
			t.Errorf("decision = %v, want %v", got, want)
		}
	}
}

// The exit codes are a contract with eval/inspect/travis_agent.py, which tells a turn that
// stopped badly (scored as a failure) from travis failing to run (a harness error).
func TestHeadlessExitCodesReportHowTheTurnStopped(t *testing.T) {
	for _, tc := range []struct {
		err  error
		want int
	}{
		{errors.New("config is broken"), 1},
		{turnStoppedError{reason: core.StopError, err: errors.New("provider 500")}, 2},
		{turnStoppedError{reason: core.StopBudget}, 3},
		{turnStoppedError{reason: core.StopLength}, 4},
		{turnStoppedError{reason: core.StopCanceled}, 2},
		{fmt.Errorf("wrapped: %w", turnStoppedError{reason: core.StopBudget}), 3},
	} {
		if got := reportExitCode(tc.err); got != tc.want {
			t.Errorf("reportExitCode(%v) = %d, want %d", tc.err, got, tc.want)
		}
	}
}

func writeAnswersFile(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "answers.json")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}
