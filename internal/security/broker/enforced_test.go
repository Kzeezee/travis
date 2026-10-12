package broker_test

import (
	"context"
	"errors"
	"io"
	"math/rand/v2"
	"os"
	"path/filepath"
	"testing"

	"github.com/Kzeezee/travis/internal/config"
	"github.com/Kzeezee/travis/internal/core"
	"github.com/Kzeezee/travis/internal/security/broker"
	"github.com/Kzeezee/travis/internal/security/capabilities"
	"github.com/Kzeezee/travis/internal/security/policy"
)

// buildSessionBroker enforces the named profile over a fresh workspace, with home and temp
// roots that are separate temp directories so the real home directory never matters.
func buildSessionBroker(t *testing.T, profile string) (*broker.PolicyEnforcedBroker, string, string) {
	t.Helper()
	base := t.TempDir()
	workspace, home := filepath.Join(base, "workspace"), filepath.Join(base, "home")
	for _, dir := range []string{workspace, home} {
		if err := os.MkdirAll(dir, 0o750); err != nil {
			t.Fatal(err)
		}
	}
	paths := config.PathSet{WorkspaceDir: workspace, StateDir: filepath.Join(home, "state")}
	sessionPolicy, err := broker.BuildSessionPolicy(profile, broker.SessionRoots{Paths: paths, Home: home, Temp: filepath.Join(base, "temp")})
	if err != nil {
		t.Fatal(err)
	}
	return broker.BuildPolicyEnforcedBroker(workspace, sessionPolicy, core.SystemClock{}), workspace, home
}

func TestPolicyEnforcedBrokerReadsGrantedWorkspaceFiles(t *testing.T) {
	enforced, workspace, _ := buildSessionBroker(t, "paranoid")
	writeWorkspaceFile(t, workspace, "notes/today.md", "hi")

	opened, err := enforced.OpenFile(context.Background(), "notes/today.md")
	if err != nil {
		t.Fatal(err)
	}
	defer opened.Content.Close()
	content, err := io.ReadAll(opened.Content)
	if err != nil || string(content) != "hi" || opened.Info.WorkspacePath != "notes/today.md" {
		t.Fatalf("OpenFile = %+v, %q, %v", opened.Info, content, err)
	}

	entries, err := enforced.ListDirectory(context.Background(), "notes")
	if err != nil || len(entries) != 1 || entries[0].WorkspacePath != "notes/today.md" {
		t.Fatalf("ListDirectory = %+v, %v", entries, err)
	}
}

func TestPolicyEnforcedBrokerRefusesWhatPolicyDenies(t *testing.T) {
	enforced, workspace, home := buildSessionBroker(t, "paranoid")
	writeWorkspaceFile(t, workspace, ".env", "TOKEN=secret")
	outside := writeWorkspaceFile(t, home, "elsewhere.txt", "not granted")

	for name, path := range map[string]string{
		"deny-listed file in the workspace": ".env",
		"file with no grant":                outside,
		"escape by dot-dot":                 "../home/elsewhere.txt",
		"missing file with no grant":        filepath.Join(home, "missing.txt"),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := enforced.OpenFile(context.Background(), path); !errors.Is(err, core.ErrDenied) {
				t.Fatalf("OpenFile(%q) error = %v, want ErrDenied", path, err)
			}
		})
	}
}

func TestPolicyEnforcedBrokerRefusesSymlinksOutOfItsGrants(t *testing.T) {
	enforced, workspace, home := buildSessionBroker(t, "paranoid")
	outside := writeWorkspaceFile(t, home, "elsewhere.txt", "not granted")
	createSymlinkOrSkip(t, outside, filepath.Join(workspace, "link.txt"))

	if _, err := enforced.OpenFile(context.Background(), "link.txt"); !errors.Is(err, core.ErrDenied) {
		t.Fatalf("OpenFile(link out of grants) error = %v, want ErrDenied", err)
	}
}

func TestBuildSessionPolicyRejectsUnknownProfiles(t *testing.T) {
	root := t.TempDir()
	_, err := broker.BuildSessionPolicy("nonsense", broker.SessionRoots{Paths: config.PathSet{WorkspaceDir: root}, Home: root, Temp: root})
	if !errors.Is(err, core.ErrInvalid) {
		t.Fatalf("BuildSessionPolicy(unknown) error = %v, want ErrInvalid", err)
	}
}

// TestPolicyEnforcedBrokerOpensAFileIffPolicyAllowsIt is §5.12.5's property: for random grants
// and deny rules, a file is opened exactly when policy allows both the written and resolved path.
func TestPolicyEnforcedBrokerOpensAFileIffPolicyAllowsIt(t *testing.T) {
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	workspace, outside := filepath.Join(base, "workspace"), filepath.Join(base, "outside")
	writeWorkspaceFile(t, workspace, "a.txt", "a")
	writeWorkspaceFile(t, workspace, "sub/b.txt", "b")
	writeWorkspaceFile(t, workspace, "sub/.env", "secret")
	outsideFile := writeWorkspaceFile(t, outside, "c.txt", "c")

	requested := []string{"a.txt", "sub/b.txt", "sub/.env", "missing.txt", "../outside/c.txt", outsideFile}
	if os.Symlink(outsideFile, filepath.Join(workspace, "link.txt")) == nil {
		requested = append(requested, "link.txt")
	}

	globUnder := func(dir string) string { return filepath.ToSlash(dir) + "/**" }
	grantChoices := []string{globUnder(workspace), globUnder(filepath.Join(workspace, "sub")), globUnder(outside)}
	denyChoices := []string{"**/.env", globUnder(filepath.Join(workspace, "sub"))}
	isCaseInsensitive := config.IsPathCaseInsensitive()

	random := rand.New(rand.NewPCG(27, 2))
	for range 200 {
		var grants capabilities.Set
		for _, scope := range grantChoices {
			if random.IntN(2) == 0 {
				grants = append(grants, core.Capability{Kind: core.CapabilityFSRead, Scope: scope})
			}
		}
		var readDeny []string
		for _, glob := range denyChoices {
			if random.IntN(2) == 0 {
				readDeny = append(readDeny, glob)
			}
		}
		sessionPolicy := policy.New(grants, readDeny, nil, isCaseInsensitive)
		enforced := broker.BuildPolicyEnforcedBroker(workspace, sessionPolicy, core.SystemClock{})
		isAllowed := func(path string) bool {
			req := policy.Request{Kind: core.CapabilityFSRead, Target: path}
			return sessionPolicy.Evaluate(context.Background(), req).Verdict == policy.Allow
		}

		for _, path := range requested {
			written := path
			if !filepath.IsAbs(path) {
				written = filepath.Join(workspace, path)
			}
			resolved, resolveErr := filepath.EvalSymlinks(written)
			shouldOpen := isAllowed(written) && resolveErr == nil && isAllowed(resolved)

			opened, err := enforced.OpenFile(context.Background(), path)
			if err == nil {
				_ = opened.Content.Close()
			}
			if isOpened := err == nil; isOpened != shouldOpen {
				t.Fatalf("grants %v, deny %v: OpenFile(%q) opened=%v err=%v, policy allows=%v",
					grants, readDeny, path, isOpened, err, shouldOpen)
			}
		}
	}
}

func TestPolicyEnforcedBrokerWritesOnlyWherePolicyAllows(t *testing.T) {
	enforced, workspace, home := buildSessionBroker(t, "default")
	ctx := context.Background()
	if _, err := enforced.WriteFile(ctx, "notes/new.md", []byte("hi")); err != nil {
		t.Fatalf("WriteFile(granted) = %v", err)
	}
	if err := os.MkdirAll(filepath.Join(home, "elsewhere"), 0o750); err != nil {
		t.Fatal(err)
	}
	createSymlinkOrSkip(t, filepath.Join(home, "elsewhere"), filepath.Join(workspace, "out"))

	for name, path := range map[string]string{
		"decision rules":          ".travis/decisions.toml",
		"git hooks":               ".git/hooks/pre-commit",
		"deny-listed secret":      ".env",
		"file with no grant":      filepath.Join(home, "x.txt"),
		"escape by dot-dot":       "../home/x.txt",
		"symlinked dir out":       "out/x.txt",
		"new dir under a symlink": "out/deeper/x.txt",
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := enforced.WriteFile(ctx, path, []byte("x")); !errors.Is(err, core.ErrDenied) {
				t.Fatalf("WriteFile(%q) error = %v, want ErrDenied", path, err)
			}
		})
	}
	if entries, _ := os.ReadDir(filepath.Join(home, "elsewhere")); len(entries) != 0 {
		t.Fatalf("a refused write still created %v", entries)
	}

	paranoid, _, _ := buildSessionBroker(t, "paranoid")
	if _, err := paranoid.WriteFile(ctx, "notes/new.md", []byte("hi")); !errors.Is(err, core.ErrDenied) {
		t.Fatalf("paranoid WriteFile error = %v, want ErrDenied", err)
	}
}

// Approvals narrow an Ask (§6.4); they can never turn a Deny, or an Allow, into a recorded grant.
func TestPolicyEnforcedBrokerOnlyApprovesWhatPolicyAsks(t *testing.T) {
	enforced, workspace, _ := buildSessionBroker(t, "default")
	ctx := context.Background()
	secret := filepath.Join(workspace, ".env")
	granted := filepath.Join(workspace, "notes.md")

	if err := enforced.ApproveOnce(ctx, core.Capability{Kind: core.CapabilityFSRead, Scope: secret}); !errors.Is(err, core.ErrDenied) {
		t.Fatalf("ApproveOnce(denied target) = %v, want ErrDenied", err)
	}
	rule := core.DecisionRule{Kind: core.CapabilityFSWrite, Scope: granted, Decision: core.DecisionAllow, Source: "test"}
	if err := enforced.AddDecisionRule(ctx, rule); !errors.Is(err, core.ErrDenied) {
		t.Fatalf("AddDecisionRule(already allowed) = %v, want ErrDenied", err)
	}
	if _, err := os.Stat(filepath.Join(workspace, ".travis", "decisions.toml")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("a refused rule still reached decisions.toml: %v", err)
	}
}

// TestPolicyEnforcedBrokerWritesAFileIffPolicyAllowsIt is the write half of §5.12.5's property.
func TestPolicyEnforcedBrokerWritesAFileIffPolicyAllowsIt(t *testing.T) {
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	workspace, outside := filepath.Join(base, "workspace"), filepath.Join(base, "outside")
	writeWorkspaceFile(t, workspace, "sub/b.txt", "b")
	writeWorkspaceFile(t, outside, "c.txt", "c")
	requested := []string{"a.txt", "sub/b.txt", "sub/new/d.txt", "sub/.env", "../outside/c.txt", filepath.Join(outside, "e.txt")}

	globUnder := func(dir string) string { return filepath.ToSlash(dir) + "/**" }
	grantChoices := []string{globUnder(workspace), globUnder(filepath.Join(workspace, "sub")), globUnder(outside)}
	denyChoices := []string{"**/.env", globUnder(filepath.Join(workspace, "sub"))}
	isCaseInsensitive := config.IsPathCaseInsensitive()

	random := rand.New(rand.NewPCG(27, 3))
	for range 200 {
		var grants capabilities.Set
		for _, scope := range grantChoices {
			if random.IntN(2) == 0 {
				grants = append(grants, core.Capability{Kind: core.CapabilityFSWrite, Scope: scope})
			}
		}
		var writeDeny []string
		for _, glob := range denyChoices {
			if random.IntN(2) == 0 {
				writeDeny = append(writeDeny, glob)
			}
		}
		sessionPolicy := policy.New(grants, nil, writeDeny, isCaseInsensitive)
		enforced := broker.BuildPolicyEnforcedBroker(workspace, sessionPolicy, core.SystemClock{})

		for _, path := range requested {
			target := path
			if !filepath.IsAbs(path) {
				target = filepath.Join(workspace, path)
			}
			target = filepath.Clean(target)
			req := policy.Request{Kind: core.CapabilityFSWrite, Target: target}
			shouldWrite := sessionPolicy.Evaluate(context.Background(), req).Verdict == policy.Allow

			_, err := enforced.WriteFile(context.Background(), path, []byte("x"))
			if isWritten := err == nil; isWritten != shouldWrite {
				t.Fatalf("grants %v, deny %v: WriteFile(%q) written=%v err=%v, policy allows=%v",
					grants, writeDeny, path, isWritten, err, shouldWrite)
			}
		}
	}
}

// An untrusted step's effect Asks (§5.12.4); the human's answer, given in that same step, lets it
// through: once for one effect, or always for the rest of the session and in decisions.toml.
func TestPolicyEnforcedBrokerApprovesAnUntrustedStepsAsk(t *testing.T) {
	enforced, workspace, _ := buildSessionBroker(t, "default")
	writeWorkspaceFile(t, workspace, "notes.md", "hi")
	writeWorkspaceFile(t, workspace, ".env", "TOKEN=secret")
	untrusted := core.MarkUntrustedStep(context.Background())
	openNotes := func() error {
		opened, err := enforced.OpenFile(untrusted, "notes.md")
		if err == nil {
			_ = opened.Content.Close()
		}
		return err
	}

	var required *core.ApprovalRequiredError
	if err := openNotes(); !errors.As(err, &required) {
		t.Fatalf("untrusted OpenFile = %v, want an approval request", err)
	}
	asked := core.Capability{Kind: required.Operation.Kind, Scope: required.Operation.Target}
	if err := enforced.ApproveOnce(context.Background(), asked); !errors.Is(err, core.ErrDenied) {
		t.Fatalf("ApproveOnce outside the untrusted step = %v, want ErrDenied: policy allows it there, so nothing asks", err)
	}
	secret := core.Capability{Kind: core.CapabilityFSRead, Scope: filepath.Join(filepath.Dir(asked.Scope), ".env")}
	if err := enforced.ApproveOnce(untrusted, secret); !errors.Is(err, core.ErrDenied) {
		t.Fatalf("ApproveOnce(deny-listed) = %v, want ErrDenied", err)
	}

	if err := enforced.ApproveOnce(untrusted, asked); err != nil {
		t.Fatalf("ApproveOnce = %v", err)
	}
	if err := openNotes(); err != nil {
		t.Fatalf("OpenFile after ApproveOnce = %v", err)
	}
	if err := openNotes(); !errors.As(err, &required) {
		t.Fatalf("second OpenFile = %v, want it to ask again: once is spent", err)
	}

	rule := core.DecisionRule{Kind: asked.Kind, Scope: asked.Scope, Decision: core.DecisionAllow, Source: "test"}
	if err := enforced.AddDecisionRule(untrusted, rule); err != nil {
		t.Fatalf("AddDecisionRule = %v", err)
	}
	for range 2 {
		if err := openNotes(); err != nil {
			t.Fatalf("OpenFile after an always rule = %v", err)
		}
	}
	if _, err := os.Stat(filepath.Join(workspace, ".travis", "decisions.toml")); err != nil {
		t.Fatalf("the always rule was not persisted: %v", err)
	}
}
