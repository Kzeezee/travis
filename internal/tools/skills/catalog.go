// Package skills loads the SKILL.md ecosystem with progressive disclosure, and a
// workspace AGENTS.md, both at the correct trust tier (DESIGN.md §5.8, D-23, R-02).
package skills

import (
	"context"
	"fmt"
	"io"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/Kzeezee/travis/internal/config"
	"github.com/Kzeezee/travis/internal/core"
)

// Tier is which trust level a skill was discovered under. There are only two: §5.8's
// illustrative third "vendor" tier and "system" root prefix have no equivalent anywhere
// else in this codebase (config.PathSet only distinguishes a user's own config dir from
// the workspace), and the ticket's own "Trust tiers" paragraph only ever names two.
type Tier int

const (
	// TierUser is a root outside the workspace: content the user put there themselves.
	TierUser Tier = iota
	// TierWorkspace is a root inside the workspace: a cloned repo's own content, which
	// the model must never be able to mistake for travis's or the user's own instructions.
	TierWorkspace
)

// String reports t the way it should read outside this package, such as in /skills.
func (t Tier) String() string {
	if t == TierWorkspace {
		return "workspace"
	}
	return "user"
}

// skill is one discovered SKILL.md. Body is stored in its final, already-wrapped form
// for workspace tier (core.WrapUntrustedContent applied once at discovery, in
// discover.go) — Open never has to remember to wrap it.
type skill struct {
	Name        string
	Description string
	Body        string
	Tier        Tier
	Root        string
}

// Catalog is every skill discovered at startup, plus the workspace AGENTS.md. Workspace
// content is held back until a human approves it once (RequestWorkspaceApproval), so a
// cloned repo's text never reaches the system prompt on `cd` alone (R-02, D-23).
type Catalog struct {
	diagnostics io.Writer
	trustPath   string
	// instructionPaths are workspace-relative, lower-cased, slash-separated paths whose text is
	// workspace instructions: AGENTS.md and every workspace skill root.
	instructionPaths []string

	mu          sync.RWMutex
	byName      map[string]skill
	agentsBlock string
	// pending is workspace content awaiting approval; nil when there is none or it loaded.
	pending *workspaceContent
}

// BuildCatalog discovers every skill under cfg's roots, classifying each root's trust
// tier by whether it resolves inside paths.WorkspaceDir (R-02), and reads the workspace
// AGENTS.md (D-23). User-tier skills load now; workspace content loads now only if its
// exact content was approved "always" before. Every failure, from a malformed SKILL.md to
// an unreadable AGENTS.md, is a line written to diagnostics, never fatal.
func BuildCatalog(cfg config.SkillsCfg, paths config.PathSet, diagnostics io.Writer) *Catalog {
	if diagnostics == nil {
		diagnostics = io.Discard
	}
	catalog := &Catalog{
		diagnostics: diagnostics,
		trustPath:   filepath.Join(paths.StateDir, workspaceTrustFileName),
		byName:      make(map[string]skill),
	}
	workspaceRoots, userRoots := classifySkillRoots(cfg, paths, diagnostics)
	for _, root := range userRoots {
		for _, discovered := range discoverSkillsUnderRoot(root, TierUser, diagnostics) {
			addResolvingDuplicates(catalog.byName, discovered, diagnostics)
		}
	}

	content := workspaceContent{dir: paths.WorkspaceDir, skills: make(map[string]skill)}
	catalog.instructionPaths = []string{strings.ToLower(agentsFileName)}
	for _, root := range workspaceRoots {
		if rel, err := filepath.Rel(paths.WorkspaceDir, root); err == nil {
			catalog.instructionPaths = append(catalog.instructionPaths, strings.ToLower(filepath.ToSlash(rel)))
		}
		for _, discovered := range discoverSkillsUnderRoot(root, TierWorkspace, diagnostics) {
			addResolvingDuplicates(content.skills, discovered, diagnostics)
		}
	}
	content.agentsBlock = loadWorkspaceAgentsBlock(paths, diagnostics)
	if content.isEmpty() {
		return catalog
	}
	content.hash = content.reportHash()
	if isWorkspaceContentTrusted(catalog.trustPath, content.hash) {
		catalog.install(content)
	} else {
		catalog.pending = &content
	}
	return catalog
}

// RequestWorkspaceApproval asks approver once whether to load the pending workspace skills
// and AGENTS.md, and loads them if allowed; "always" remembers a hash of their content and
// folders, so an edit or the same repo at another path asks again. It blocks until answered, so callers run it beside the frontend.
func (c *Catalog) RequestWorkspaceApproval(ctx context.Context, approver core.Approver) {
	c.mu.RLock()
	pending := c.pending
	c.mu.RUnlock()
	if pending == nil || approver == nil {
		return
	}
	decision, err := approver.RequestApproval(ctx, core.Operation{
		Kind:        core.CapabilityFSRead,
		Target:      pending.describe(),
		Explanation: "add this repo's instructions to every prompt, from " + pending.dir,
	})
	if err != nil || decision == core.ApprovalDenied {
		return
	}
	if decision == core.ApprovalAlways {
		if err := trustWorkspaceContent(c.trustPath, pending.hash); err != nil {
			fmt.Fprintf(c.diagnostics, "skills: remembering workspace approval: %v\n", err)
		}
	}
	c.install(*pending)
}

// install makes approved workspace content live; a workspace skill shadows a user skill of
// the same name (workspace > user).
func (c *Catalog) install(content workspaceContent) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for name, next := range content.skills {
		if existing, taken := c.byName[name]; taken {
			fmt.Fprintf(c.diagnostics, "skills: %q from %s shadowed by %q from %s (workspace beats user)\n",
				existing.Name, existing.Root, next.Name, next.Root)
		}
		c.byName[name] = next
	}
	c.agentsBlock = content.agentsBlock
	c.pending = nil
}

// ReportIsWorkspaceInstructionPath reports whether workspacePath (relative, slash-separated)
// holds workspace instructions, so a tool reading it can mark its output untrusted even when
// the human never approved loading it. Case-insensitive, which only ever over-marks.
func (c *Catalog) ReportIsWorkspaceInstructionPath(workspacePath string) bool {
	path := strings.ToLower(workspacePath)
	for _, instruction := range c.instructionPaths {
		if instruction == "." || path == instruction || strings.HasPrefix(path, instruction+"/") {
			return true
		}
	}
	return false
}

// addResolvingDuplicates keeps the first entry claimed for a name and warns about every later one.
func addResolvingDuplicates(byName map[string]skill, next skill, diagnostics io.Writer) {
	if existing, taken := byName[next.Name]; taken {
		fmt.Fprintf(diagnostics, "skills: %q from %s shadowed by the earlier %q from %s\n",
			next.Name, next.Root, existing.Name, existing.Root)
		return
	}
	byName[next.Name] = next
}

// open returns a loaded skill on demand (progressive disclosure level 2).
func (c *Catalog) open(name string) (skill, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	found, ok := c.byName[name]
	return found, ok
}

// SkillSummary is one catalog entry's identity for display outside this package (/skills),
// without exposing a skill's body or trust-tier wrapping.
type SkillSummary struct {
	Name        string
	Description string
	Tier        Tier
}

// ReportSkillSummaries lists every loaded skill, sorted by name.
func (c *Catalog) ReportSkillSummaries() []SkillSummary {
	c.mu.RLock()
	defer c.mu.RUnlock()
	names := make([]string, 0, len(c.byName))
	for name := range c.byName {
		names = append(names, name)
	}
	sort.Strings(names)

	summaries := make([]SkillSummary, 0, len(names))
	for _, name := range names {
		s := c.byName[name]
		summaries = append(summaries, SkillSummary{Name: s.Name, Description: s.Description, Tier: s.Tier})
	}
	return summaries
}

// reportIndex builds the always-on system-prompt section (progressive disclosure level
// 1): one line per skill, ~30 tokens each. Workspace-tier entries are never mixed into
// the trusted block — even a one-line name+description sits inside its own
// core.WrapUntrustedContent block, because the adversarial regression this ticket exists
// to prevent is a hostile description reaching the model outside any delimiter at all.
func (c *Catalog) reportIndex() string {
	names := make([]string, 0, len(c.byName))
	for name := range c.byName {
		names = append(names, name)
	}
	sort.Strings(names)

	var trusted, untrusted []string
	for _, name := range names {
		s := c.byName[name]
		line := fmt.Sprintf("- %s: %s", s.Name, s.Description)
		if s.Tier == TierWorkspace {
			untrusted = append(untrusted, line)
		} else {
			trusted = append(trusted, line)
		}
	}

	var sections []string
	if len(trusted) > 0 {
		sections = append(sections, "Available skills (call skill_open with a name for the full instructions):\n"+strings.Join(trusted, "\n"))
	}
	if len(untrusted) > 0 {
		block := strings.Join(untrusted, "\n")
		sections = append(sections, core.WrapUntrustedContent("workspace:skills", "Available workspace skills:\n"+block))
	}
	return strings.Join(sections, "\n\n")
}

// ReportSystemPromptSections implements internal/agent/loop's SkillsIndex port: the
// loop appends whatever this returns to its own fixed sections, never inspecting trust
// tiers or delimiters itself.
func (c *Catalog) ReportSystemPromptSections() []string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	var sections []string
	if index := c.reportIndex(); index != "" {
		sections = append(sections, index)
	}
	if c.agentsBlock != "" {
		sections = append(sections, c.agentsBlock)
	}
	return sections
}
