package broker_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Kzeezee/travis/internal/config"
	"github.com/Kzeezee/travis/internal/core"
	"github.com/Kzeezee/travis/internal/security/broker"
)

// helperModeEnvVar names what TestExecHelperProcess should do when the broker runs this test
// binary as a command. It is the os/exec package's own pattern for a portable child process:
// no shell, no fixture binary, and the same behaviour on all three OSes.
const helperModeEnvVar = "TRAVIS_EXEC_TEST_HELPER_MODE"

const canaryAPIKey = "sk-exec-canary"

// TestExecHelperProcess is not a test: it is the program the exec tests run. It only does
// anything when the broker started it with helperModeEnvVar set.
func TestExecHelperProcess(t *testing.T) {
	mode := os.Getenv(helperModeEnvVar)
	if mode == "" {
		t.Skip("this test is the child process the exec tests run")
	}
	switch mode {
	case "print_args":
		// Each argument on its own line, so a shell having split or expanded one would show.
		for _, arg := range os.Args[1:] {
			fmt.Println(arg)
		}
	case "print_cwd":
		dir, err := os.Getwd()
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(3)
		}
		fmt.Println(dir)
	case "print_api_key":
		fmt.Printf("OPENAI_API_KEY=%q\n", os.Getenv("OPENAI_API_KEY"))
	case "write_marker":
		if err := os.WriteFile("exec-marker.txt", []byte("the command ran"), 0o600); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(3)
		}
	case "sleep":
		time.Sleep(time.Minute)
	case "fail":
		fmt.Fprintln(os.Stderr, "deliberate failure")
		os.Exit(7)
	case "flood":
		line := strings.Repeat("x", 1024) + "\n"
		for written := 0; written < 4*broker.MaxCommandOutputBytes; written += len(line) {
			fmt.Print(line)
		}
	}
	os.Exit(0)
}

// buildHelperArgv is the argv that runs TestExecHelperProcess in mode, plus extra arguments
// the helper will echo back.
func buildHelperArgv(t *testing.T, mode string, extra ...string) []string {
	t.Helper()
	t.Setenv(helperModeEnvVar, mode)
	argv := []string{os.Args[0], "-test.run=^TestExecHelperProcess$"}
	return append(argv, extra...)
}

// buildExecBroker is a policy-enforcing broker over a fresh workspace whose exec grants are
// execAllow; no grant at all means exec is denied outright.
func buildExecBroker(t *testing.T, execAllow ...string) (*broker.PolicyEnforcedBroker, string) {
	t.Helper()
	root := t.TempDir()
	workspace := filepath.Join(root, "workspace")
	if err := os.MkdirAll(workspace, 0o700); err != nil {
		t.Fatal(err)
	}
	paths := config.PathSet{WorkspaceDir: workspace, StateDir: filepath.Join(root, "state")}
	sessionPolicy, err := broker.BuildSessionPolicy("default", broker.SessionRoots{
		Paths: paths, Home: filepath.Join(root, "home"), Temp: filepath.Join(root, "temp"), ExecAllow: execAllow,
	})
	if err != nil {
		t.Fatal(err)
	}
	return broker.BuildPolicyEnforcedBroker(workspace, sessionPolicy, discardRecords(), nil, "", core.SystemClock{}), workspace
}

// approveAndRun runs argv the way a client does: the first attempt asks, the human's answer is
// reported, and only then does the command run.
func approveAndRun(t *testing.T, effects *broker.PolicyEnforcedBroker, argv []string) core.CommandResult {
	t.Helper()
	ctx := context.Background()
	_, err := effects.RunCommand(ctx, argv)
	var required *core.ApprovalRequiredError
	if !errors.As(err, &required) {
		t.Fatalf("first RunCommand = %v, want an approval request", err)
	}
	if required.Operation.Kind != core.CapabilityExec || required.Operation.Target != argv[0] {
		t.Fatalf("approval operation = %+v, want exec on %q", required.Operation, argv[0])
	}
	// D-27: the question is asked as irreversible, which is what keeps the approval modal from
	// offering an always answer for it.
	if !required.Operation.IsIrreversible {
		t.Fatalf("approval operation = %+v, want it marked irreversible", required.Operation)
	}
	if err := effects.ApproveOnce(ctx, core.Capability{Kind: core.CapabilityExec, Scope: argv[0]}); err != nil {
		t.Fatal(err)
	}
	result, err := effects.RunCommand(ctx, argv)
	if err != nil {
		t.Fatalf("approved RunCommand = %v", err)
	}
	return result
}

func TestRunCommandPassesArgvLiterallyWithNoShell(t *testing.T) {
	effects, _ := buildExecBroker(t, "**")
	// Every one of these is a shell metacharacter; a shell would expand, split or swallow them.
	literals := []string{"$HOME", "*", "a b", "`id`", "a;b", "a|b", "$(id)", "~", "a&&b", ">out.txt"}
	result := approveAndRun(t, effects, buildHelperArgv(t, "print_args", literals...))

	echoed := strings.Split(strings.TrimSpace(strings.ReplaceAll(result.Output, "\r\n", "\n")), "\n")
	for _, literal := range literals {
		if !containsExactLine(echoed, literal) {
			t.Fatalf("argument %q did not arrive literally; the command saw %q", literal, echoed)
		}
	}
	if result.ExitCode != 0 {
		t.Fatalf("exit code = %d, want 0", result.ExitCode)
	}
}

func containsExactLine(lines []string, want string) bool {
	for _, line := range lines {
		if line == want {
			return true
		}
	}
	return false
}

func TestRunCommandRunsInTheWorkspace(t *testing.T) {
	effects, workspace := buildExecBroker(t, "**")
	result := approveAndRun(t, effects, buildHelperArgv(t, "print_cwd"))

	reported := strings.TrimSpace(result.Output)
	resolvedWorkspace, err := filepath.EvalSymlinks(workspace)
	if err != nil {
		t.Fatal(err)
	}
	if reported != resolvedWorkspace {
		t.Fatalf("command cwd = %q, want the workspace %q", reported, resolvedWorkspace)
	}
}

func TestRunCommandHidesTheProviderCredential(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", canaryAPIKey)
	effects, _ := buildExecBroker(t, "**")
	result := approveAndRun(t, effects, buildHelperArgv(t, "print_api_key"))

	if strings.Contains(result.Output, canaryAPIKey) {
		t.Fatalf("the command saw the provider credential: %q", result.Output)
	}
}

func TestRunCommandReportsANonZeroExitAsAResult(t *testing.T) {
	effects, _ := buildExecBroker(t, "**")
	result := approveAndRun(t, effects, buildHelperArgv(t, "fail"))

	if result.ExitCode != 7 {
		t.Fatalf("exit code = %d, want 7", result.ExitCode)
	}
	if !strings.Contains(result.Output, "deliberate failure") {
		t.Fatalf("output = %q, want the command's stderr", result.Output)
	}
}

// The production cap is commandTimeout, which no test should wait out; an earlier deadline on
// the request exercises the same kill-and-report path, since the run inherits whichever
// deadline comes first.
func TestRunCommandKillsACommandAtItsDeadlineAndReportsIt(t *testing.T) {
	effects, _ := buildExecBroker(t, "**")
	argv := buildHelperArgv(t, "sleep")
	ctx := context.Background()
	if _, err := effects.RunCommand(ctx, argv); !errors.As(err, new(*core.ApprovalRequiredError)) {
		t.Fatalf("RunCommand = %v, want an approval request", err)
	}
	if err := effects.ApproveOnce(ctx, core.Capability{Kind: core.CapabilityExec, Scope: argv[0]}); err != nil {
		t.Fatal(err)
	}

	deadlined, cancel := context.WithTimeout(ctx, 200*time.Millisecond)
	defer cancel()
	started := time.Now()
	result, err := effects.RunCommand(deadlined, argv)
	if err != nil {
		t.Fatalf("RunCommand = %v, want a timed-out result", err)
	}
	if !result.IsTimedOut {
		t.Fatalf("result = %+v, want IsTimedOut", result)
	}
	if elapsed := time.Since(started); elapsed > 30*time.Second {
		t.Fatalf("the killed command held the broker for %v", elapsed)
	}
}

func TestRunCommandCapsTheOutputItCollects(t *testing.T) {
	effects, _ := buildExecBroker(t, "**")
	result := approveAndRun(t, effects, buildHelperArgv(t, "flood"))

	if len(result.Output) > broker.MaxCommandOutputBytes {
		t.Fatalf("collected %d bytes, want at most %d", len(result.Output), broker.MaxCommandOutputBytes)
	}
	if !result.IsOutputTruncated {
		t.Fatal("a flood of output was not reported as truncated")
	}
}

// A missing grant is a refusal, not a question: nothing is put to the human and nothing runs.
func TestRunCommandWithNoGrantIsDeniedAndRunsNothing(t *testing.T) {
	effects, workspace := buildExecBroker(t)
	argv := buildHelperArgv(t, "write_marker")

	_, err := effects.RunCommand(context.Background(), argv)
	if !errors.Is(err, core.ErrDenied) {
		t.Fatalf("RunCommand without a grant = %v, want denied", err)
	}
	var required *core.ApprovalRequiredError
	if errors.As(err, &required) {
		t.Fatal("a command with no grant was put to the human instead of refused")
	}
	assertMarkerAbsent(t, workspace)
}

// The grant covers "git" alone, so the command the model actually named stays denied — the
// same spelling rule an MCP launch grant uses.
func TestRunCommandOutsideTheGrantIsDeniedAndRunsNothing(t *testing.T) {
	effects, workspace := buildExecBroker(t, "git")
	argv := buildHelperArgv(t, "write_marker")

	if _, err := effects.RunCommand(context.Background(), argv); !errors.Is(err, core.ErrDenied) {
		t.Fatalf("RunCommand outside the grant = %v, want denied", err)
	}
	assertMarkerAbsent(t, workspace)
}

func TestRunCommandDeniedByTheHumanRunsNothing(t *testing.T) {
	effects, workspace := buildExecBroker(t, "**")
	argv := buildHelperArgv(t, "write_marker")

	// The client never reports an approval, which is what a denied modal looks like here.
	_, err := effects.RunCommand(context.Background(), argv)
	var required *core.ApprovalRequiredError
	if !errors.As(err, &required) {
		t.Fatalf("RunCommand = %v, want an approval request", err)
	}
	assertMarkerAbsent(t, workspace)
}

// D-27: a grant plus one approval buys exactly one command. The next one asks again.
func TestRunCommandAsksAgainAfterEveryApprovedRun(t *testing.T) {
	effects, _ := buildExecBroker(t, "**")
	argv := buildHelperArgv(t, "print_cwd")
	approveAndRun(t, effects, argv)

	_, err := effects.RunCommand(context.Background(), argv)
	var required *core.ApprovalRequiredError
	if !errors.As(err, &required) {
		t.Fatalf("second RunCommand = %v, want another approval request", err)
	}
}

// An always rule can never exist for exec (D-27), so one cannot be used to skip the question.
func TestRunCommandCannotBeRelaxedByAnAlwaysRule(t *testing.T) {
	effects, _ := buildExecBroker(t, "**")
	argv := buildHelperArgv(t, "print_cwd")
	rule := core.DecisionRule{Kind: core.CapabilityExec, Scope: argv[0], Decision: core.DecisionAllow, Source: "test"}

	if err := effects.AddDecisionRule(context.Background(), rule); !errors.Is(err, core.ErrDenied) {
		t.Fatalf("AddDecisionRule for exec = %v, want denied", err)
	}
	_, err := effects.RunCommand(context.Background(), argv)
	if !errors.As(err, new(*core.ApprovalRequiredError)) {
		t.Fatalf("RunCommand after the refused rule = %v, want an approval request", err)
	}
}

func TestRunCommandRejectsAnArgvItCannotRun(t *testing.T) {
	effects, _ := buildExecBroker(t, "**")
	for name, argv := range map[string][]string{
		"empty":         {},
		"empty program": {""},
		"too many args": append([]string{"git"}, make([]string, broker.MaxCommandArgs)...),
		"oversize arg":  {"git", strings.Repeat("a", broker.MaxCommandArgBytes+1)},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := effects.RunCommand(context.Background(), argv); !errors.Is(err, core.ErrInvalid) {
				t.Fatalf("RunCommand %q = %v, want invalid", name, err)
			}
		})
	}
}

func TestRunCommandReportsAMissingProgram(t *testing.T) {
	effects, _ := buildExecBroker(t, "**")
	argv := []string{"travis-no-such-program-exists"}

	ctx := context.Background()
	if _, err := effects.RunCommand(ctx, argv); !errors.As(err, new(*core.ApprovalRequiredError)) {
		t.Fatalf("RunCommand = %v, want an approval request", err)
	}
	if err := effects.ApproveOnce(ctx, core.Capability{Kind: core.CapabilityExec, Scope: argv[0]}); err != nil {
		t.Fatal(err)
	}
	if _, err := effects.RunCommand(ctx, argv); !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("RunCommand for a missing program = %v, want not found", err)
	}
}

// The approval round trip over the wire: the client asks the human, reports the answer, and
// retries, all for one RunCommand call.
func TestRemoteBrokerRunsACommandOnlyAfterTheHumanApproves(t *testing.T) {
	for name, decision := range map[string]core.ApprovalDecision{"once": core.ApprovalOnce, "deny": core.ApprovalDenied} {
		t.Run(name, func(t *testing.T) {
			effects, workspace := buildExecBroker(t, "**")
			argv := buildHelperArgv(t, "write_marker")
			events := &recordingSink{}
			approver := &scriptedApprover{decision: decision}
			remote, _ := startPipedBrokerWith(t, effects, broker.RemoteOptions{Events: events, Approver: approver})

			ctx := core.WithToolCallID(context.Background(), "call-exec")
			result, err := remote.RunCommand(ctx, argv)
			if len(approver.asked) != 1 {
				t.Fatalf("the human was asked %d times, want once", len(approver.asked))
			}
			if decision == core.ApprovalDenied {
				if !errors.Is(err, core.ErrDenied) {
					t.Fatalf("denied RunCommand = %v, want denied", err)
				}
				assertMarkerAbsent(t, workspace)
				assertExecDenialPublished(t, events.events, argv[0])
				return
			}
			if err != nil {
				t.Fatalf("approved RunCommand = %v", err)
			}
			if result.ExitCode != 0 {
				t.Fatalf("exit code = %d, want 0", result.ExitCode)
			}
			if _, err := os.Stat(filepath.Join(workspace, "exec-marker.txt")); err != nil {
				t.Fatalf("the approved command left no marker: %v", err)
			}
		})
	}
}

func assertMarkerAbsent(t *testing.T, workspace string) {
	t.Helper()
	if _, err := os.Stat(filepath.Join(workspace, "exec-marker.txt")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("the refused command ran: marker exists (%v)", err)
	}
}

func assertExecDenialPublished(t *testing.T, events []core.Event, program string) {
	t.Helper()
	for _, event := range events {
		denied, isDenial := event.(core.PolicyDenied)
		if isDenial && denied.Capability.Kind == core.CapabilityExec && denied.Capability.Scope == program {
			return
		}
	}
	t.Fatalf("no PolicyDenied names exec on %q: %+v", program, events)
}
