package broker_test

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/pelletier/go-toml/v2"

	"github.com/Kzeezee/travis/internal/core"
	"github.com/Kzeezee/travis/internal/security/broker"
)

func writeWorkspaceFile(t *testing.T, root, rel, content string) string {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func createSymlinkOrSkip(t *testing.T, target, link string) {
	t.Helper()
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlinks unavailable here: %v", err)
	}
}

func TestLocalBrokerDescribesPathsRelativeToTheWorkspace(t *testing.T) {
	root := t.TempDir()
	writeWorkspaceFile(t, root, "notes/today.md", "hi")
	local := broker.BuildLocalBroker(root, core.SystemClock{})

	file, err := local.DescribePath(context.Background(), "notes/today.md")
	if err != nil {
		t.Fatal(err)
	}
	if file.Kind != core.PathKindFile || file.WorkspacePath != "notes/today.md" || !filepath.IsAbs(file.Path) {
		t.Fatalf("DescribePath(file) = %+v", file)
	}

	dir, err := local.DescribePath(context.Background(), ".")
	if err != nil {
		t.Fatal(err)
	}
	if dir.Kind != core.PathKindDirectory || dir.WorkspacePath != "." {
		t.Fatalf("DescribePath(root) = %+v", dir)
	}

	again, err := local.DescribePath(context.Background(), file.Path)
	if err != nil || again != file {
		t.Fatalf("DescribePath(reported path) = %+v, %v; want %+v", again, err, file)
	}
}

func TestLocalBrokerDeniesPathsOutsideTheWorkspace(t *testing.T) {
	parent := t.TempDir()
	root := filepath.Join(parent, "workspace")
	writeWorkspaceFile(t, root, "inside.txt", "in")
	outside := writeWorkspaceFile(t, parent, "outside.txt", "out")
	local := broker.BuildLocalBroker(root, core.SystemClock{})

	for _, path := range []string{"../outside.txt", "../missing.txt", outside} {
		t.Run(path, func(t *testing.T) {
			if _, err := local.DescribePath(context.Background(), path); !errors.Is(err, core.ErrDenied) {
				t.Fatalf("DescribePath(%q) error = %v, want ErrDenied", path, err)
			}
			if _, err := local.OpenFile(context.Background(), path); !errors.Is(err, core.ErrDenied) {
				t.Fatalf("OpenFile(%q) error = %v, want ErrDenied", path, err)
			}
		})
	}
}

func TestLocalBrokerDeniesSymlinksThatEscapeTheWorkspace(t *testing.T) {
	parent := t.TempDir()
	root := filepath.Join(parent, "workspace")
	writeWorkspaceFile(t, root, "inside.txt", "in")
	outside := writeWorkspaceFile(t, parent, "outside.txt", "out")
	createSymlinkOrSkip(t, outside, filepath.Join(root, "escape.txt"))
	local := broker.BuildLocalBroker(root, core.SystemClock{})

	if _, err := local.OpenFile(context.Background(), "escape.txt"); !errors.Is(err, core.ErrDenied) {
		t.Fatalf("OpenFile(escape.txt) error = %v, want ErrDenied", err)
	}
}

func TestLocalBrokerReportsMissingPathsAndDirectories(t *testing.T) {
	root := t.TempDir()
	writeWorkspaceFile(t, root, "folder/file.txt", "x")
	local := broker.BuildLocalBroker(root, core.SystemClock{})

	if _, err := local.OpenFile(context.Background(), "missing.txt"); !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("OpenFile(missing) error = %v, want ErrNotFound", err)
	}
	if _, err := local.OpenFile(context.Background(), "folder"); !errors.Is(err, core.ErrIsDirectory) {
		t.Fatalf("OpenFile(folder) error = %v, want ErrIsDirectory", err)
	}
	if _, err := local.ListDirectory(context.Background(), "missing"); !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("ListDirectory(missing) error = %v, want ErrNotFound", err)
	}
}

func TestLocalBrokerOpensFilesWithTheirResolvedInfo(t *testing.T) {
	root := t.TempDir()
	writeWorkspaceFile(t, root, "a.txt", "contents")
	local := broker.BuildLocalBroker(root, core.SystemClock{})

	opened, err := local.OpenFile(context.Background(), "workspace:a.txt")
	if err != nil {
		t.Fatal(err)
	}
	defer opened.Content.Close()
	data, err := io.ReadAll(opened.Content)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "contents" || opened.Info.WorkspacePath != "a.txt" {
		t.Fatalf("OpenFile = %q, %+v", data, opened.Info)
	}
}

func TestLocalBrokerListsEntriesInNameOrderWithoutFollowingSymlinks(t *testing.T) {
	root := t.TempDir()
	writeWorkspaceFile(t, root, "b.txt", "b")
	writeWorkspaceFile(t, root, "a/inner.txt", "a")
	createSymlinkOrSkip(t, root, filepath.Join(root, "c-loop"))
	local := broker.BuildLocalBroker(root, core.SystemClock{})

	entries, err := local.ListDirectory(context.Background(), ".")
	if err != nil {
		t.Fatal(err)
	}
	want := []struct {
		workspacePath string
		kind          core.PathKind
	}{{"a", core.PathKindDirectory}, {"b.txt", core.PathKindFile}, {"c-loop", core.PathKindSymlink}}
	if len(entries) != len(want) {
		t.Fatalf("ListDirectory = %+v, want %d entries", entries, len(want))
	}
	for i, entry := range entries {
		if entry.WorkspacePath != want[i].workspacePath || entry.Kind != want[i].kind {
			t.Errorf("entry %d = %+v, want %s (%s)", i, entry, want[i].workspacePath, want[i].kind)
		}
	}
}

func TestLocalBrokerRejectsUnknownPathPrefixesAsInvalid(t *testing.T) {
	local := broker.BuildLocalBroker(t.TempDir(), core.SystemClock{})
	if _, err := local.DescribePath(context.Background(), "secrets:token"); !errors.Is(err, core.ErrInvalid) {
		t.Fatalf("DescribePath(secrets:token) error = %v, want ErrInvalid", err)
	}
}

func TestLocalBrokerStopsWhenTheContextIsCancelled(t *testing.T) {
	root := t.TempDir()
	writeWorkspaceFile(t, root, "a.txt", "a")
	local := broker.BuildLocalBroker(root, core.SystemClock{})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := local.OpenFile(ctx, "a.txt"); !errors.Is(err, context.Canceled) {
		t.Fatalf("OpenFile error = %v, want context.Canceled", err)
	}
	if _, err := local.ListDirectory(ctx, "."); !errors.Is(err, context.Canceled) {
		t.Fatalf("ListDirectory error = %v, want context.Canceled", err)
	}
}

// decisionRuleDocument mirrors the broker's own on-disk shape so the test can parse back
// what AddDecisionRule wrote without reaching into the package's unexported type.
type decisionRuleDocument struct {
	Rule []core.DecisionRule `toml:"rule"`
}

func readDecisionRules(t *testing.T, root string) []core.DecisionRule {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(root, ".travis", "decisions.toml"))
	if err != nil {
		t.Fatal(err)
	}
	var doc decisionRuleDocument
	if err := toml.Unmarshal(data, &doc); err != nil {
		t.Fatalf("decisions.toml did not parse: %v\n%s", err, data)
	}
	return doc.Rule
}

func TestLocalBrokerAddDecisionRuleAppendsAParseableRule(t *testing.T) {
	root := t.TempDir()
	local := broker.BuildLocalBroker(root, core.SystemClock{})
	rule := core.DecisionRule{Kind: core.CapabilityFSWrite, Scope: filepath.Join(root, "notes/today.md"), Decision: core.DecisionAllow, Source: "user approved"}

	if err := local.AddDecisionRule(context.Background(), rule); err != nil {
		t.Fatal(err)
	}

	rules := readDecisionRules(t, root)
	if len(rules) != 1 || rules[0].Kind != rule.Kind || rules[0].Scope != rule.Scope || rules[0].Decision != rule.Decision {
		t.Fatalf("decisions.toml = %+v, want one rule matching %+v", rules, rule)
	}
	if rules[0].AddedAt.IsZero() {
		t.Fatal("AddDecisionRule did not stamp AddedAt")
	}
}

func TestLocalBrokerAddDecisionRuleAccumulatesAcrossCalls(t *testing.T) {
	root := t.TempDir()
	local := broker.BuildLocalBroker(root, core.SystemClock{})
	for i := 0; i < 2; i++ {
		rule := core.DecisionRule{Kind: core.CapabilityFSWrite, Scope: filepath.Join(root, "f.txt"), Decision: core.DecisionAllow}
		if err := local.AddDecisionRule(context.Background(), rule); err != nil {
			t.Fatal(err)
		}
	}
	if rules := readDecisionRules(t, root); len(rules) != 2 {
		t.Fatalf("decisions.toml has %d rules, want 2: %+v", len(rules), rules)
	}
}

func TestLocalBrokerAddDecisionRuleRejectsExec(t *testing.T) {
	local := broker.BuildLocalBroker(t.TempDir(), core.SystemClock{})
	rule := core.DecisionRule{Kind: core.CapabilityExec, Scope: "git push", Decision: core.DecisionAllow}
	if err := local.AddDecisionRule(context.Background(), rule); !errors.Is(err, core.ErrDenied) {
		t.Fatalf("AddDecisionRule(exec) error = %v, want ErrDenied", err)
	}
}

func TestLocalBrokerAddDecisionRuleRejectsMalformedRules(t *testing.T) {
	root := t.TempDir()
	local := broker.BuildLocalBroker(root, core.SystemClock{})
	cases := map[string]core.DecisionRule{
		"empty kind":      {Scope: "a", Decision: core.DecisionAllow},
		"empty scope":     {Kind: core.CapabilityFSWrite, Decision: core.DecisionAllow},
		"unknown outcome": {Kind: core.CapabilityFSWrite, Scope: "a", Decision: "deny"},
	}
	for name, rule := range cases {
		t.Run(name, func(t *testing.T) {
			if err := local.AddDecisionRule(context.Background(), rule); !errors.Is(err, core.ErrInvalid) {
				t.Fatalf("AddDecisionRule(%+v) error = %v, want ErrInvalid", rule, err)
			}
		})
	}
}

// TestLocalBrokerAddDecisionRuleSerializesConcurrentWriters proves the exclusive lock
// (D-32): N goroutines appending at once must still leave N well-formed entries, never a
// corrupted interleaving of two writes.
func TestLocalBrokerAddDecisionRuleSerializesConcurrentWriters(t *testing.T) {
	root := t.TempDir()
	local := broker.BuildLocalBroker(root, core.SystemClock{})
	const writers = 8

	var wg sync.WaitGroup
	for i := 0; i < writers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			rule := core.DecisionRule{Kind: core.CapabilityFSWrite, Scope: filepath.Join(root, "f.txt"), Decision: core.DecisionAllow, Source: "writer"}
			if err := local.AddDecisionRule(context.Background(), rule); err != nil {
				t.Error(err)
			}
		}(i)
	}
	wg.Wait()

	if rules := readDecisionRules(t, root); len(rules) != writers {
		t.Fatalf("decisions.toml has %d rules, want %d: %+v", len(rules), writers, rules)
	}
}

func readWorkspaceFile(t *testing.T, root, rel string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestLocalBrokerWritesNewFilesAndMissingParents(t *testing.T) {
	root := t.TempDir()
	local := broker.BuildLocalBroker(root, core.SystemClock{})

	info, err := local.WriteFile(context.Background(), "notes/2026/summary.md", []byte("new"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Kind != core.PathKindFile || info.WorkspacePath != "notes/2026/summary.md" {
		t.Fatalf("WriteFile info = %+v", info)
	}
	if got := readWorkspaceFile(t, root, "notes/2026/summary.md"); got != "new" {
		t.Fatalf("written content = %q", got)
	}
}

func TestLocalBrokerOverwritesKeepingPermissionsAndLeavesNoTempFiles(t *testing.T) {
	root := t.TempDir()
	path := writeWorkspaceFile(t, root, "a.txt", "old")
	before, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	local := broker.BuildLocalBroker(root, core.SystemClock{})

	if _, err := local.WriteFile(context.Background(), "a.txt", []byte("replaced")); err != nil {
		t.Fatal(err)
	}
	after, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := readWorkspaceFile(t, root, "a.txt"); got != "replaced" {
		t.Fatalf("content = %q", got)
	}
	if after.Mode().Perm() != before.Mode().Perm() {
		t.Fatalf("perm = %v, want %v", after.Mode().Perm(), before.Mode().Perm())
	}
	leftovers, err := filepath.Glob(filepath.Join(root, ".travis-write-*"))
	if err != nil || len(leftovers) != 0 {
		t.Fatalf("temp leftovers = %v, %v", leftovers, err)
	}
}

func TestLocalBrokerDeniesWritesOutsideTheWorkspace(t *testing.T) {
	parent := t.TempDir()
	root := filepath.Join(parent, "workspace")
	writeWorkspaceFile(t, root, "inside.txt", "in")
	local := broker.BuildLocalBroker(root, core.SystemClock{})

	for _, path := range []string{"../outside.txt", filepath.Join(parent, "abs.txt"), "../new/dir/file.txt"} {
		if _, err := local.WriteFile(context.Background(), path, []byte("x")); !errors.Is(err, core.ErrDenied) {
			t.Fatalf("WriteFile(%q) error = %v, want ErrDenied", path, err)
		}
	}
	entries, err := os.ReadDir(parent)
	if err != nil || len(entries) != 1 {
		t.Fatalf("parent entries = %v, %v; want only the workspace", entries, err)
	}
}

func TestLocalBrokerDeniesWritesThroughASymlinkedParentThatEscapes(t *testing.T) {
	parent := t.TempDir()
	root := filepath.Join(parent, "workspace")
	outsideDir := filepath.Join(parent, "outside")
	writeWorkspaceFile(t, root, "inside.txt", "in")
	if err := os.MkdirAll(outsideDir, 0o750); err != nil {
		t.Fatal(err)
	}
	createSymlinkOrSkip(t, outsideDir, filepath.Join(root, "escape"))
	local := broker.BuildLocalBroker(root, core.SystemClock{})

	for _, path := range []string{"escape/new.txt", "escape/deeper/new.txt"} {
		if _, err := local.WriteFile(context.Background(), path, []byte("x")); !errors.Is(err, core.ErrDenied) {
			t.Fatalf("WriteFile(%q) error = %v, want ErrDenied", path, err)
		}
	}
	if entries, _ := os.ReadDir(outsideDir); len(entries) != 0 {
		t.Fatalf("outside dir gained entries %v", entries)
	}
}

func TestLocalBrokerRefusesToWriteOverDirectoriesAndSymlinks(t *testing.T) {
	root := t.TempDir()
	target := writeWorkspaceFile(t, root, "folder/real.txt", "real")
	local := broker.BuildLocalBroker(root, core.SystemClock{})

	if _, err := local.WriteFile(context.Background(), "folder", []byte("x")); !errors.Is(err, core.ErrIsDirectory) {
		t.Fatalf("WriteFile(folder) error = %v, want ErrIsDirectory", err)
	}
	createSymlinkOrSkip(t, target, filepath.Join(root, "link.txt"))
	if _, err := local.WriteFile(context.Background(), "link.txt", []byte("x")); !errors.Is(err, core.ErrInvalid) {
		t.Fatalf("WriteFile(link.txt) error = %v, want ErrInvalid", err)
	}
	if got := readWorkspaceFile(t, root, "folder/real.txt"); got != "real" {
		t.Fatalf("symlink target changed to %q", got)
	}
}

func TestLocalBrokerWritesNothingWhenTheContextIsCancelled(t *testing.T) {
	root := t.TempDir()
	local := broker.BuildLocalBroker(root, core.SystemClock{})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := local.WriteFile(ctx, "a.txt", []byte("x")); !errors.Is(err, context.Canceled) {
		t.Fatalf("WriteFile error = %v, want context.Canceled", err)
	}
	if _, err := os.Stat(filepath.Join(root, "a.txt")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("a.txt exists after cancelled write: %v", err)
	}
}
