// Package skills loads the SKILL.md ecosystem with progressive disclosure, and a
// workspace AGENTS.md, both at the correct trust tier (DESIGN.md §5.8, D-23, R-02).
package skills

import (
	"fmt"
	"io"
	"sort"
	"strings"

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

// Catalog is every skill discovered at startup, plus the workspace AGENTS.md if one was
// loaded. It is built once and never mutated — unlike mcp.Manager, there is no
// subprocess, no supervision, and no approval wait, so there is nothing to Close.
type Catalog struct {
	byName      map[string]skill
	agentsBlock string
}

// BuildCatalog discovers every skill under cfg's roots, classifying each root's trust
// tier by whether it resolves inside paths.WorkspaceDir (R-02), and loads the workspace
// AGENTS.md (D-23). Workspace-tier content — roots and AGENTS.md alike — is skipped
// entirely unless cfg.LoadWorkspace is set: no approval UX exists yet (ticket 2D) to ask
// before injecting a cloned repo's text into the system prompt. Every failure, from a
// malformed SKILL.md to an unreadable AGENTS.md, is a line written to diagnostics, never
// fatal to building the catalog.
func BuildCatalog(cfg config.SkillsCfg, paths config.PathSet, diagnostics io.Writer) *Catalog {
	if diagnostics == nil {
		diagnostics = io.Discard
	}
	catalog := &Catalog{byName: make(map[string]skill)}
	for _, discovered := range discoverSkills(cfg, paths, diagnostics) {
		catalog.addResolvingDuplicates(discovered, diagnostics)
	}
	catalog.agentsBlock = loadWorkspaceAgentsBlock(cfg, paths, diagnostics)
	return catalog
}

// addResolvingDuplicates keeps the first entry claimed for a name and warns about every
// later one, so discoverSkills' own ordering (workspace roots before user roots) is what
// decides precedence — matching the ticket's "workspace > user > system" rule with the
// system tier dropped (see Tier's doc comment).
func (c *Catalog) addResolvingDuplicates(next skill, diagnostics io.Writer) {
	if existing, taken := c.byName[next.Name]; taken {
		fmt.Fprintf(diagnostics, "skills: %q from %s shadowed by the earlier %q from %s (workspace beats user)\n",
			next.Name, next.Root, existing.Name, existing.Root)
		return
	}
	c.byName[next.Name] = next
}

// open returns a skill's full body on demand (progressive disclosure level 2).
func (c *Catalog) open(name string) (string, bool) {
	found, ok := c.byName[name]
	return found.Body, ok
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
	var sections []string
	if index := c.reportIndex(); index != "" {
		sections = append(sections, index)
	}
	if c.agentsBlock != "" {
		sections = append(sections, c.agentsBlock)
	}
	return sections
}
