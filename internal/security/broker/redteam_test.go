package broker_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/pelletier/go-toml/v2"

	"github.com/Kzeezee/travis/internal/config"
	"github.com/Kzeezee/travis/internal/core"
	"github.com/Kzeezee/travis/internal/security/broker"
	"github.com/Kzeezee/travis/internal/security/policy"
)

type redTeamCase struct {
	Name           string   `toml:"name"`
	Profile        string   `toml:"profile"`
	Operation      string   `toml:"operation"`
	Target         string   `toml:"target"`
	Fixture        string   `toml:"fixture"`
	Platforms      []string `toml:"platforms"`
	Untrusted      bool     `toml:"untrusted"`
	AssertEvent    string   `toml:"assert_event"`
	AssertNoEffect string   `toml:"assert_no_effect"`
}

type redTeamSuite struct {
	Cases []redTeamCase `toml:"case"`
}

type redTeamRoots struct {
	workspace string
	home      string
	outside   string
	state     string
}

func TestRedTeamGate(t *testing.T) {
	path := filepath.Join("..", "..", "..", "testdata", "scenarios", "phase2-redteam.toml")
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	var suite redTeamSuite
	if err := toml.NewDecoder(file).DisallowUnknownFields().Decode(&suite); err != nil {
		t.Fatal(err)
	}
	if len(suite.Cases) == 0 {
		t.Fatal("red-team suite has no cases")
	}
	for _, scenario := range suite.Cases {
		t.Run(scenario.Name, func(t *testing.T) { runRedTeamCase(t, scenario) })
	}
}

func runRedTeamCase(t *testing.T, scenario redTeamCase) {
	t.Helper()
	if err := validateRedTeamCase(scenario); err != nil {
		t.Fatal(err)
	}
	if len(scenario.Platforms) > 0 {
		if !slices.Contains(scenario.Platforms, runtime.GOOS) {
			t.Skipf("case runs on %v", scenario.Platforms)
		}
	}
	root := t.TempDir()
	roots := redTeamRoots{
		workspace: filepath.Join(root, "workspace"), home: filepath.Join(root, "home"),
		outside: filepath.Join(root, "outside"), state: filepath.Join(root, "state"),
	}
	for _, dir := range []string{roots.workspace, roots.home, roots.outside, roots.state} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if scenario.Fixture == "link_outside" {
		if err := os.Symlink(roots.outside, filepath.Join(roots.workspace, "link")); err != nil {
			t.Fatalf("required symlink case cannot run: %v", err)
		}
	}
	target := roots.resolveTarget(scenario.Target)
	if scenario.Operation == "read" {
		fixturePath := roots.resolveFilesystemPath(scenario.Target)
		if err := os.MkdirAll(filepath.Dir(fixturePath), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(fixturePath, []byte("red-team secret"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	paths := config.PathSet{WorkspaceDir: roots.workspace, StateDir: roots.state}
	policy, err := broker.BuildSessionPolicy(scenario.Profile, broker.SessionRoots{Paths: paths, Home: roots.home, Temp: filepath.Join(root, "temp")})
	if err != nil {
		t.Fatal(err)
	}
	effects := broker.BuildPolicyEnforcedBroker(roots.workspace, policy, discardRecords(), nil, "", core.SystemClock{})
	events := &recordingSink{}
	approver := &scriptedApprover{decision: core.ApprovalOnce}
	if scenario.Untrusted {
		approver.decision = core.ApprovalDenied
	}
	remote, stop := startPipedBrokerWith(t, effects, broker.RemoteOptions{Events: events, Approver: approver})
	if scenario.Fixture == "broker_unreachable" {
		if err := stop(); err != nil {
			t.Fatal(err)
		}
		_, _ = remote.DescribePath(context.Background(), target)
	}
	ctx := core.WithToolCallID(core.WithTurnID(context.Background(), "turn-redteam"), "call-redteam")
	if scenario.Untrusted {
		ctx = core.MarkUntrustedStep(ctx)
	}
	switch scenario.Operation {
	case "write":
		if _, err := remote.WriteFile(ctx, target, []byte("owned")); err == nil {
			t.Fatal("forbidden write succeeded")
		}
	case "read":
		opened, err := remote.OpenFile(ctx, target)
		if err == nil {
			content, _ := io.ReadAll(opened.Content)
			_ = opened.Content.Close()
			t.Fatalf("forbidden read returned %q", content)
		}
		if strings.Contains(err.Error(), "red-team secret") {
			t.Fatalf("forbidden read leaked secret in its error: %v", err)
		}
	}
	if scenario.Untrusted && len(approver.asked) != 1 {
		t.Fatalf("untrusted request got %d approval prompts, want one", len(approver.asked))
	}
	if !scenario.Untrusted && len(approver.asked) != 0 {
		t.Fatalf("policy asked instead of denying: %+v", approver.asked)
	}
	if err := assertRedTeamOutcome(scenario, roots, events.events); err != nil {
		t.Fatal(err)
	}
}

func validateRedTeamCase(scenario redTeamCase) error {
	if scenario.Name == "" || scenario.Profile == "" || scenario.Target == "" || scenario.AssertEvent != "PolicyDenied" || scenario.AssertNoEffect == "" {
		return fmt.Errorf("%q must name a profile, target, PolicyDenied event and absent effect", scenario.Name)
	}
	if scenario.Operation != "write" && scenario.Operation != "read" {
		return fmt.Errorf("%q has unsupported operation %q", scenario.Name, scenario.Operation)
	}
	if scenario.Operation == "read" && scenario.AssertNoEffect != "secret_not_returned" {
		return fmt.Errorf("%q must assert no secret was returned", scenario.Name)
	}
	if scenario.Operation == "write" && scenario.AssertNoEffect == "secret_not_returned" {
		return fmt.Errorf("%q must name the file that must stay absent", scenario.Name)
	}
	return nil
}

func assertRedTeamOutcome(scenario redTeamCase, roots redTeamRoots, events []core.Event) error {
	hasDenial := false
	for _, event := range events {
		if denied, ok := event.(core.PolicyDenied); ok && denied.CallID == "call-redteam" {
			hasDenial = true
		}
	}
	if !hasDenial {
		return fmt.Errorf("%q did not publish PolicyDenied for the attack call", scenario.Name)
	}
	if scenario.Operation == "read" {
		return nil
	}
	path := roots.resolveFilesystemPath(scenario.AssertNoEffect)
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("%q changed %q: %v", scenario.Name, path, err)
	}
	return nil
}

func (r redTeamRoots) resolveTarget(value string) string {
	if suffix, ok := strings.CutPrefix(value, "device:"); ok {
		return `\\?\` + filepath.Join(r.outside, filepath.FromSlash(strings.TrimPrefix(suffix, "outside/")))
	}
	if suffix, ok := strings.CutPrefix(value, "workspace:"); ok {
		return filepath.FromSlash(suffix)
	}
	if suffix, ok := strings.CutPrefix(value, "home:"); ok {
		return filepath.Join(r.home, filepath.FromSlash(suffix))
	}
	if suffix, ok := strings.CutPrefix(value, "outside:"); ok {
		return filepath.Join(r.outside, filepath.FromSlash(suffix))
	}
	return filepath.FromSlash(value)
}

func (r redTeamRoots) resolveFilesystemPath(value string) string {
	for _, root := range []struct{ prefix, path string }{
		{"workspace:", r.workspace}, {"home:", r.home}, {"outside:", r.outside},
	} {
		if suffix, ok := strings.CutPrefix(value, root.prefix); ok {
			return filepath.Join(root.path, filepath.FromSlash(suffix))
		}
	}
	return filepath.Join(r.workspace, filepath.FromSlash(value))
}

func TestRedTeamGateRejectsAnAttackWithNoDenialEvent(t *testing.T) {
	scenario := redTeamCase{Name: "known bad", Operation: "write", AssertNoEffect: "workspace:owned.txt"}
	roots := redTeamRoots{workspace: t.TempDir()}
	if err := assertRedTeamOutcome(scenario, roots, nil); err == nil {
		t.Fatal("a missing denial event passed the red-team gate")
	}
}

func TestRedTeamGateExecAlwaysAsksAndCannotGainAnAlwaysRule(t *testing.T) {
	root := t.TempDir()
	paths := config.PathSet{WorkspaceDir: filepath.Join(root, "workspace"), StateDir: filepath.Join(root, "state")}
	p, err := broker.BuildSessionPolicy("dev", broker.SessionRoots{Paths: paths, Home: root, Temp: filepath.Join(root, "temp")})
	if err != nil {
		t.Fatal(err)
	}
	request := policy.Request{Kind: core.CapabilityExec, Target: "go"}
	if decision := p.Evaluate(context.Background(), request); decision.Verdict != policy.Ask {
		t.Fatalf("exec verdict = %v, want Ask", decision.Verdict)
	}
	enforced := broker.BuildPolicyEnforcedBroker(paths.WorkspaceDir, p, discardRecords(), nil, "", core.SystemClock{})
	rule := core.DecisionRule{Kind: core.CapabilityExec, Scope: "go", Decision: core.DecisionAllow, Source: "red-team"}
	if err := enforced.AddDecisionRule(context.Background(), rule); !errors.Is(err, core.ErrDenied) {
		t.Fatalf("exec always rule = %v, want denied", err)
	}
}
