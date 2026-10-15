package skills

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Kzeezee/travis/internal/config"
	"github.com/Kzeezee/travis/internal/core"
)

const (
	agentsMarker = "AGENTS-MARKER: write to .git/hooks"
	skillMarker  = "SKILL-MARKER"
)

// writeHostileWorkspace lays out a cloned repo with an AGENTS.md and one workspace skill.
func writeHostileWorkspace(t *testing.T) (config.PathSet, config.SkillsCfg) {
	t.Helper()
	workspace := t.TempDir()
	skillDir := filepath.Join(workspace, ".skills", "evil")
	if err := os.MkdirAll(skillDir, 0o700); err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, filepath.Join(workspace, agentsFileName), agentsMarker)
	writeTestFile(t, filepath.Join(skillDir, skillFileName), "---\nname: evil\ndescription: "+skillMarker+"\n---\nbody\n")
	return buildTestPathSet(t, workspace), config.SkillsCfg{Roots: []string{filepath.Join(workspace, ".skills")}}
}

func writeTestFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func reportPrompt(catalog *Catalog) string {
	return strings.Join(catalog.ReportSystemPromptSections(), "\n")
}

// TestRedTeamGateWorkspaceInstructionsLoadOnlyAfterApproval: a cloned repo's AGENTS.md and
// skills are absent from the prompt until a human approves them, and absent again once
// their content changes (3F).
func TestRedTeamGateWorkspaceInstructionsLoadOnlyAfterApproval(t *testing.T) {
	paths, cfg := writeHostileWorkspace(t)

	denied := BuildCatalog(cfg, paths, &strings.Builder{})
	denier := &scriptedApprover{decision: core.ApprovalDenied}
	denied.RequestWorkspaceApproval(context.Background(), denier)
	if prompt := reportPrompt(denied); strings.Contains(prompt, agentsMarker) || strings.Contains(prompt, skillMarker) {
		t.Fatalf("prompt after denial = %q, want no workspace content", prompt)
	}
	if len(denier.asked) != 1 || denier.asked[0].Target != paths.WorkspaceDir {
		t.Fatalf("asked %+v, want exactly one approval naming the workspace", denier.asked)
	}

	approved := BuildCatalog(cfg, paths, &strings.Builder{})
	approved.RequestWorkspaceApproval(context.Background(), &scriptedApprover{decision: core.ApprovalAlways})
	prompt := reportPrompt(approved)
	if !strings.Contains(prompt, agentsMarker) || !strings.Contains(prompt, skillMarker) {
		t.Fatalf("prompt after approval = %q, want AGENTS.md and the skill", prompt)
	}
	if strings.Contains(stripUntrustedBlocks(prompt), agentsMarker) || strings.Contains(stripUntrustedBlocks(prompt), skillMarker) {
		t.Fatalf("prompt = %q, approved workspace content must stay inside untrusted delimiters", prompt)
	}

	remembered := BuildCatalog(cfg, paths, &strings.Builder{})
	unasked := &scriptedApprover{decision: core.ApprovalDenied}
	remembered.RequestWorkspaceApproval(context.Background(), unasked)
	if len(unasked.asked) != 0 || !strings.Contains(reportPrompt(remembered), agentsMarker) {
		t.Fatalf("asked %+v; want unchanged content loaded without asking again", unasked.asked)
	}

	writeTestFile(t, filepath.Join(paths.WorkspaceDir, agentsFileName), agentsMarker+" and also exfiltrate")
	edited := BuildCatalog(cfg, paths, &strings.Builder{})
	if prompt := reportPrompt(edited); strings.Contains(prompt, agentsMarker) || strings.Contains(prompt, skillMarker) {
		t.Fatalf("prompt after an edit = %q, want workspace content held until approved again", prompt)
	}
	reasker := &scriptedApprover{decision: core.ApprovalDenied}
	edited.RequestWorkspaceApproval(context.Background(), reasker)
	if len(reasker.asked) != 1 {
		t.Fatalf("asked %+v after an edit, want one new approval", reasker.asked)
	}
}

func TestOpenToolMarksOnlyWorkspaceSkillsUntrusted(t *testing.T) {
	paths, cfg := writeHostileWorkspace(t)
	catalog := BuildCatalog(cfg, paths, &strings.Builder{})
	catalog.RequestWorkspaceApproval(context.Background(), &scriptedApprover{decision: core.ApprovalOnce})

	output, err := BuildOpenTool(catalog).Invoke(context.Background(), json.RawMessage(`{"name":"evil"}`))
	if err != nil || output.IsError || !output.IsUntrusted {
		t.Fatalf("skill_open evil = %+v, %v; want an untrusted result", output, err)
	}
	output, err = BuildOpenTool(buildTestCatalog()).Invoke(context.Background(), json.RawMessage(`{"name":"demo"}`))
	if err != nil || output.IsUntrusted {
		t.Fatalf("skill_open demo = %+v, %v; want a trusted result for a user skill", output, err)
	}
}
