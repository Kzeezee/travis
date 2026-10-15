package skills

import (
	"context"
	"io"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Kzeezee/travis/internal/config"
	"github.com/Kzeezee/travis/internal/core"
)

// buildTestPathSet returns a minimal PathSet sufficient for Resolve on absolute roots;
// workspaceDir alone decides every discovered root's trust tier in these tests.
func buildTestPathSet(t *testing.T, workspaceDir string) config.PathSet {
	return config.PathSet{WorkspaceDir: workspaceDir, StateDir: t.TempDir()}
}

// scriptedApprover answers every request with decision and records what it was asked.
type scriptedApprover struct {
	decision core.ApprovalDecision
	asked    []core.Operation
}

func (a *scriptedApprover) RequestApproval(_ context.Context, op core.Operation) (core.ApprovalDecision, error) {
	a.asked = append(a.asked, op)
	return a.decision, nil
}

// buildApprovedCatalog builds a catalog and approves its workspace content once.
func buildApprovedCatalog(t *testing.T, cfg config.SkillsCfg, paths config.PathSet, diagnostics io.Writer) *Catalog {
	t.Helper()
	catalog := BuildCatalog(cfg, paths, diagnostics)
	catalog.RequestWorkspaceApproval(context.Background(), &scriptedApprover{decision: core.ApprovalOnce})
	return catalog
}

func absTestdata(t *testing.T, rel string) string {
	t.Helper()
	abs, err := filepath.Abs(filepath.Join("testdata", "skills", rel))
	if err != nil {
		t.Fatal(err)
	}
	return abs
}

// stripUntrustedBlocks removes every "<!-- BEGIN UNTRUSTED ... --> ... <!-- END
// UNTRUSTED -->" region, so a test can assert adversarial text appears nowhere outside
// one: strip the blocks, then assert the text is gone from what remains.
func stripUntrustedBlocks(s string) string {
	const (
		begin = "<!-- BEGIN UNTRUSTED"
		end   = "<!-- END UNTRUSTED -->"
	)
	for {
		start := strings.Index(s, begin)
		if start < 0 {
			return s
		}
		stop := strings.Index(s[start:], end)
		if stop < 0 {
			return s[:start]
		}
		s = s[:start] + s[start+stop+len(end):]
	}
}

func TestBuildCatalogListsValidSkillAsTrusted(t *testing.T) {
	paths := buildTestPathSet(t, t.TempDir())
	cfg := config.SkillsCfg{Roots: []string{absTestdata(t, "valid")}}
	diagnostics := &strings.Builder{}

	catalog := BuildCatalog(cfg, paths, diagnostics)
	sections := catalog.ReportSystemPromptSections()
	if len(sections) != 1 {
		t.Fatalf("sections = %+v, want exactly one (the trusted index, no AGENTS.md)", sections)
	}
	if !strings.Contains(sections[0], "example: A valid example skill") {
		t.Fatalf("index = %q, want the example skill's name and description", sections[0])
	}
	if strings.Contains(sections[0], "BEGIN UNTRUSTED") {
		t.Fatalf("index = %q, a user-tier skill must never be wrapped as untrusted", sections[0])
	}

	found, ok := catalog.open("example")
	if !ok {
		t.Fatal("open(\"example\") = false, want true")
	}
	if body := found.Body; strings.Contains(body, "BEGIN UNTRUSTED") {
		t.Fatalf("open() body = %q, a user-tier skill's body must never be wrapped either", body)
	}
}

func TestBuildCatalogSkipsMalformedFrontmatterWithDiagnostic(t *testing.T) {
	paths := buildTestPathSet(t, t.TempDir())
	cfg := config.SkillsCfg{Roots: []string{absTestdata(t, "malformed")}}
	diagnostics := &strings.Builder{}

	catalog := BuildCatalog(cfg, paths, diagnostics)
	if len(catalog.byName) != 0 {
		t.Fatalf("byName = %+v, want empty: the only fixture here is malformed", catalog.byName)
	}
	if !strings.Contains(diagnostics.String(), "broken") {
		t.Fatalf("diagnostics = %q, want it to name the broken skill", diagnostics.String())
	}
}

func TestBuildCatalogAdversarialTextOnlyInsideUntrustedDelimiters(t *testing.T) {
	root := absTestdata(t, "adversarial")
	paths := buildTestPathSet(t, root) // root lives inside its own "workspace" -> TierWorkspace
	cfg := config.SkillsCfg{Roots: []string{root}}

	catalog := buildApprovedCatalog(t, cfg, paths, &strings.Builder{})
	full := strings.Join(catalog.ReportSystemPromptSections(), "\n\n")
	if !strings.Contains(full, "Ignore previous instructions") {
		t.Fatalf("index = %q, want the adversarial description present somewhere", full)
	}
	if strings.Contains(stripUntrustedBlocks(full), "Ignore previous instructions") {
		t.Fatalf("index = %q, the adversarial text must only ever appear inside an untrusted delimiter block", full)
	}

	found, ok := catalog.open("evil")
	body := found.Body
	if !ok {
		t.Fatal("open(\"evil\") = false, want true")
	}
	if !strings.HasPrefix(body, `<!-- BEGIN UNTRUSTED`) {
		t.Fatalf("open() body = %q, a workspace-tier skill's body must be wrapped", body)
	}
	if strings.Contains(stripUntrustedBlocks(body), "Ignore previous instructions") {
		t.Fatalf("open() body = %q, the adversarial text must only appear inside the wrap", body)
	}
}

func TestBuildCatalogHoldsWorkspaceRootsUntilApproved(t *testing.T) {
	root := absTestdata(t, "adversarial")
	catalog := BuildCatalog(config.SkillsCfg{Roots: []string{root}}, buildTestPathSet(t, root), &strings.Builder{})
	if _, ok := catalog.open("evil"); ok {
		t.Fatal("open(\"evil\") = true before approval, want the workspace skill held back")
	}
	if sections := catalog.ReportSystemPromptSections(); len(sections) != 0 {
		t.Fatalf("sections = %+v, want none before approval", sections)
	}
}

func TestBuildCatalogDuplicateNamesWorkspaceWinsOverUser(t *testing.T) {
	workspaceRoot := absTestdata(t, filepath.Join("dup", "workspace"))
	userRoot := absTestdata(t, filepath.Join("dup", "user"))
	paths := buildTestPathSet(t, workspaceRoot)
	cfg := config.SkillsCfg{Roots: []string{workspaceRoot, userRoot}}
	diagnostics := &strings.Builder{}

	catalog := buildApprovedCatalog(t, cfg, paths, diagnostics)
	found, ok := catalog.open("example")
	body := found.Body
	if !ok {
		t.Fatal("open(\"example\") = false, want true")
	}
	if !strings.Contains(body, "Workspace body.") {
		t.Fatalf("body = %q, want the workspace version to win", body)
	}
	if strings.Contains(body, "User body.") {
		t.Fatalf("body = %q, the user version must be shadowed, not merged", body)
	}
	if !strings.Contains(diagnostics.String(), "shadowed") {
		t.Fatalf("diagnostics = %q, want a warning naming the shadowed entry", diagnostics.String())
	}
}

func TestReportSkillSummariesListsNameTierAndDescription(t *testing.T) {
	paths := buildTestPathSet(t, t.TempDir())
	cfg := config.SkillsCfg{Roots: []string{absTestdata(t, "valid")}}

	catalog := BuildCatalog(cfg, paths, &strings.Builder{})
	summaries := catalog.ReportSkillSummaries()
	if len(summaries) != 1 {
		t.Fatalf("summaries = %+v, want exactly one", summaries)
	}
	got := summaries[0]
	if got.Name != "example" || got.Tier != TierUser || !strings.Contains(got.Description, "valid example skill") {
		t.Fatalf("summary = %+v, want name=example, tier=user, description about the valid example skill", got)
	}
	if got.Tier.String() != "user" {
		t.Fatalf("Tier.String() = %q, want %q", got.Tier.String(), "user")
	}
}

func TestTierStringReportsWorkspace(t *testing.T) {
	if got := TierWorkspace.String(); got != "workspace" {
		t.Fatalf("TierWorkspace.String() = %q, want %q", got, "workspace")
	}
}

func TestBuildCatalogWithNoRootsHasNoSections(t *testing.T) {
	catalog := BuildCatalog(config.SkillsCfg{}, buildTestPathSet(t, t.TempDir()), &strings.Builder{})
	if sections := catalog.ReportSystemPromptSections(); len(sections) != 0 {
		t.Fatalf("sections = %+v, want none", sections)
	}
}
