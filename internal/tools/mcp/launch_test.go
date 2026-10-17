package mcp

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/Kzeezee/travis/internal/config"
	"github.com/Kzeezee/travis/internal/core"
	"github.com/Kzeezee/travis/internal/security/sandbox"
)

// recordingSink keeps every published event; connects publish from their own goroutines.
type recordingSink struct {
	mu     sync.Mutex
	events []core.Event
}

func (s *recordingSink) Publish(_ context.Context, event core.Event) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = append(s.events, event)
}

func (s *recordingSink) reportStartedTiers() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	var tiers []string
	for _, event := range s.events {
		if started, ok := event.(core.MCPServerStarted); ok {
			tiers = append(tiers, started.Tier)
		}
	}
	return tiers
}

// buildUnsupportedSandbox stands in for an OS with no sandbox backend.
func buildUnsupportedSandbox(context.Context, sandbox.Spec) (*exec.Cmd, sandbox.Tier, error) {
	return nil, sandbox.TierNone, core.ErrUnsupported
}

func connectAndClose(t *testing.T, process *serverProcess) error {
	t.Helper()
	session, err := process.connect(t.Context())
	if err == nil {
		_ = session.Close()
	}
	return err
}

func TestRedTeamGateSandboxedServerCannotReadOutsideItsGrant(t *testing.T) {
	secret := filepath.Join(t.TempDir(), "secret.txt")
	if err := os.WriteFile(secret, []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	approver := &fakeApprover{decision: core.ApprovalOnce}
	process, _ := buildTestProcess(t, buildTestServerConfig("-must-not-read", secret), grantedExecPolicy(), approver)
	if err := connectAndClose(t, process); err != nil {
		t.Fatalf("sandboxed fake server failed to start, or read %s: %v", secret, err)
	}
}

func TestSandboxedServerHomeIsItsScratchFolder(t *testing.T) {
	approver := &fakeApprover{decision: core.ApprovalOnce}
	process, _ := buildTestProcess(t, buildTestServerConfig(), grantedExecPolicy(), approver)
	process.cfg.Args = []string{"-require-home", process.scratchDir}
	if err := connectAndClose(t, process); err != nil {
		t.Fatalf("HOME is not the scratch folder: %v", err)
	}
}

func TestStartedServerReportsItsSandboxTier(t *testing.T) {
	approver := &fakeApprover{decision: core.ApprovalOnce}
	process, _ := buildTestProcess(t, buildTestServerConfig(), grantedExecPolicy(), approver)
	sink := &recordingSink{}
	process.events = sink
	if err := connectAndClose(t, process); err != nil {
		t.Fatal(err)
	}
	if tiers := sink.reportStartedTiers(); len(tiers) != 1 || tiers[0] == string(sandbox.TierNone) {
		t.Fatalf("started tiers = %v, want one real sandbox tier", tiers)
	}
	if asked := approver.askedOps(); len(asked) != 1 || !strings.Contains(asked[0].Explanation, "sandbox: ") {
		t.Fatalf("approval %+v does not name the sandbox tier", asked)
	}
}

func TestRequiredSandboxRefusesAServerThisOSCannotConfine(t *testing.T) {
	approver := &fakeApprover{decision: core.ApprovalOnce}
	process, _ := buildTestProcess(t, buildTestServerConfig(), grantedExecPolicy(), approver)
	process.buildSandboxedCommand = buildUnsupportedSandbox
	err := connectAndClose(t, process)
	if !errors.Is(err, core.ErrUnsupported) || !strings.Contains(err.Error(), "require_sandbox = false") {
		t.Fatalf("connect = %v, want an ErrUnsupported refusal naming require_sandbox", err)
	}
	if len(approver.askedOps()) != 0 {
		t.Fatal("a refused launch must not ask for approval")
	}
}

func TestUnrequiredSandboxRunsTheServerUnconfinedAtTierNone(t *testing.T) {
	approver := &fakeApprover{decision: core.ApprovalOnce}
	process, _ := buildTestProcess(t, buildTestServerConfig(), grantedExecPolicy(), approver)
	process.sandboxing.IsRequired = false
	process.buildSandboxedCommand = buildUnsupportedSandbox
	sink := &recordingSink{}
	process.events = sink
	if err := connectAndClose(t, process); err != nil {
		t.Fatal(err)
	}
	if tiers := sink.reportStartedTiers(); !slices.Equal(tiers, []string{string(sandbox.TierNone)}) {
		t.Fatalf("started tiers = %v, want [none]", tiers)
	}
}

func TestTierChangeAsksAgainDespiteRememberedTrust(t *testing.T) {
	approver := &fakeApprover{decision: core.ApprovalAlways}
	process, _ := buildTestProcess(t, buildTestServerConfig(), grantedExecPolicy(), approver)
	if err := connectAndClose(t, process); err != nil {
		t.Fatal(err)
	}
	if err := connectAndClose(t, process); err != nil || len(approver.askedOps()) != 1 {
		t.Fatalf("same tier asked %d times (%v), want trust remembered after one", len(approver.askedOps()), err)
	}
	process.sandboxing.IsRequired = false
	process.buildSandboxedCommand = buildUnsupportedSandbox
	if err := connectAndClose(t, process); err != nil || len(approver.askedOps()) != 2 {
		t.Fatalf("tier change asked %d times in total (%v), want a second approval", len(approver.askedOps()), err)
	}
}

// The launcher is re-verified before every launch, so a binary swapped mid-session is refused at the next restart.
func TestLauncherIsResolvedBeforeEveryLaunch(t *testing.T) {
	approver := &fakeApprover{decision: core.ApprovalOnce}
	process, _ := buildTestProcess(t, buildTestServerConfig(), grantedExecPolicy(), approver)
	resolved := 0
	process.sandboxing.ResolveLauncher = func() (string, error) {
		resolved++
		if resolved > 1 {
			return "", core.ErrDenied
		}
		return os.Executable()
	}
	if err := connectAndClose(t, process); err != nil {
		t.Fatal(err)
	}
	if err := connectAndClose(t, process); !errors.Is(err, core.ErrDenied) || resolved != 2 {
		t.Fatalf("second launch = %v after %d resolves, want the failed verification to refuse it", err, resolved)
	}
}

func TestBuildServerEnvLetsLaterLayersWinWithUniqueKeys(t *testing.T) {
	env := buildServerEnv(map[string]string{"HOME": "/scratch", "A": "1"}, map[string]string{"HOME": "/configured"})
	var homes []string
	for _, entry := range env {
		if value, ok := strings.CutPrefix(entry, "HOME="); ok {
			homes = append(homes, value)
		}
	}
	if !slices.Equal(homes, []string{"/configured"}) || !slices.Contains(env, "A=1") {
		t.Fatalf("HOME entries = %v in %v, want exactly the configured one", homes, env)
	}
}

func TestBuildSandboxSpecConfinesWritesToScratchAndConfiguredPaths(t *testing.T) {
	scratch, extra := t.TempDir(), t.TempDir()
	spec := buildSandboxSpec("/launcher", "/opt/runner/bin/npx", scratch, config.MCPServerCfg{Args: []string{"-y", "pkg"}, AllowNet: true, WritePaths: []string{extra}})
	if !slices.Equal(spec.Argv, []string{"/opt/runner/bin/npx", "-y", "pkg"}) || spec.Dir != scratch || !spec.AllowNetwork {
		t.Fatalf("spec = %+v", spec)
	}
	if !slices.Equal(spec.WritePaths, []string{scratch, extra}) {
		t.Fatalf("write paths = %v, want only scratch and the configured path", spec.WritePaths)
	}
}
