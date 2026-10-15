// Package skills loads the SKILL.md ecosystem with progressive disclosure, and a
// workspace AGENTS.md, both at the correct trust tier (DESIGN.md §5.8, D-23, R-02).
package skills

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
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
	ContentHash [sha256.Size]byte
}

// Catalog exposes approved snapshots, rechecking workspace instructions before each model request.
type Catalog struct {
	mu           sync.RWMutex
	byName       map[string]skill
	agentsBlock  string
	cfg          config.SkillsCfg
	paths        config.PathSet
	diagnostics  io.Writer
	approver     core.Approver
	decisionHash [sha256.Size]byte
	hasDecision  bool
	isApproved   bool
}

// BuildCatalog loads user skills; workspace approval waits until the frontend can answer it.
func BuildCatalog(cfg config.SkillsCfg, paths config.PathSet, diagnostics io.Writer, approver core.Approver) *Catalog {
	if diagnostics == nil {
		diagnostics = io.Discard
	}
	catalog := &Catalog{cfg: cfg, paths: paths, diagnostics: diagnostics, approver: approver}
	catalog.replaceSnapshot(discoverSkills(cfg, paths, diagnostics), "", false)
	return catalog
}

// BuildSystemPromptSections approves the exact workspace snapshot once per session before exposing it.
func (c *Catalog) BuildSystemPromptSections(ctx context.Context) ([]string, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	discovered := discoverSkills(c.cfg, c.paths, c.diagnostics)
	agentsBlock := loadWorkspaceAgentsBlock(c.paths, c.diagnostics)
	var workspaceSkills []skill
	for _, s := range discovered {
		if s.Tier == TierWorkspace {
			workspaceSkills = append(workspaceSkills, s)
		}
	}
	encoded, err := json.Marshal(struct {
		Skills []skill
		Agents string
	}{workspaceSkills, agentsBlock})
	if err != nil {
		return nil, fmt.Errorf("hash workspace instructions: %w", err)
	}
	fingerprint := sha256.Sum256(encoded)
	if !c.hasDecision || c.decisionHash != fingerprint {
		c.replaceSnapshot(discovered, "", false)
		c.isApproved = false
		if (len(workspaceSkills) > 0 || agentsBlock != "") && c.approver != nil {
			decision, err := c.approver.RequestApproval(ctx, core.Operation{
				Kind:           core.CapabilityFSRead,
				Target:         c.paths.WorkspaceDir,
				Explanation:    fmt.Sprintf("load workspace AGENTS.md and %d skills for this session (SHA-256 %x); changed content asks again", len(workspaceSkills), fingerprint),
				IsIrreversible: true,
			})
			if err != nil {
				return nil, err
			}
			c.isApproved = decision == core.ApprovalOnce
		}
		c.decisionHash, c.hasDecision = fingerprint, true
	}
	c.replaceSnapshot(discovered, agentsBlock, c.isApproved)
	return c.ReportSystemPromptSections(), nil
}

func (c *Catalog) replaceSnapshot(discovered []skill, agentsBlock string, isApproved bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.byName = make(map[string]skill)
	c.agentsBlock = ""
	for _, s := range discovered {
		if s.Tier == TierUser || isApproved {
			c.addResolvingDuplicates(s, c.diagnostics)
		}
	}
	if isApproved {
		c.agentsBlock = agentsBlock
	}
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

// selectSkill returns one approved snapshot, including the trust tier of its body.
func (c *Catalog) selectSkill(name string) (skill, bool) {
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

// ReportSystemPromptSections reports only the most recently approved snapshot.
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
