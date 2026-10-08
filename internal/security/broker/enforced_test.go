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
func buildSessionBroker(t *testing.T, profile string) (*broker.LocalBroker, string, string) {
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
	return broker.BuildPolicyEnforcedBroker(workspace, sessionPolicy), workspace, home
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
		enforced := broker.BuildPolicyEnforcedBroker(workspace, sessionPolicy)
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
