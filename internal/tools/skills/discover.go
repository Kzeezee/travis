package skills

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/Kzeezee/travis/internal/config"
	"github.com/Kzeezee/travis/internal/core"
)

const skillFileName = "SKILL.md"

// skillNamePattern keeps names, which reach the approval modal and the prompt, free of
// control characters and markup.
var skillNamePattern = regexp.MustCompile(`^[a-zA-Z0-9_-]{1,64}$`)

// discoverSkills orders workspace candidates first so approved workspace entries win duplicates.
func discoverSkills(cfg config.SkillsCfg, paths config.PathSet, diagnostics io.Writer) []skill {
	var workspaceRoots, userRoots []string
	for _, root := range cfg.Roots {
		resolved, err := paths.Resolve(root)
		if err != nil {
			fmt.Fprintf(diagnostics, "skills: root %q: %v\n", root, err)
			continue
		}
		if isWithinWorkspace(resolved, paths.WorkspaceDir) {
			workspaceRoots = append(workspaceRoots, resolved)
		} else {
			userRoots = append(userRoots, resolved)
		}
	}

	var discovered []skill
	for _, root := range workspaceRoots {
		discovered = append(discovered, discoverSkillsUnderRoot(root, TierWorkspace, paths.WorkspaceDir, diagnostics)...)
	}
	for _, root := range userRoots {
		discovered = append(discovered, discoverSkillsUnderRoot(root, TierUser, paths.WorkspaceDir, diagnostics)...)
	}
	sort.SliceStable(discovered, func(i, j int) bool { return discovered[i].Tier > discovered[j].Tier })
	return discovered
}

// isWithinWorkspace decides trust tier by where a root actually lives, not by which
// config.PathSet.Resolve prefix produced it (R-02 is about workspace-owned content,
// regardless of how the path was spelled).
func isWithinWorkspace(path, workspaceDir string) bool {
	if isPathWithinRoot(path, workspaceDir) {
		return true
	}
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		path = resolved
	}
	if resolved, err := filepath.EvalSymlinks(workspaceDir); err == nil {
		workspaceDir = resolved
	}
	return isPathWithinRoot(path, workspaceDir)
}

func isPathWithinRoot(path, root string) bool {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}

// discoverSkillsUnderRoot walks one directory level: root/<skill-name>/SKILL.md,
// matching the real SKILL.md ecosystem convention. A missing root is the common case
// (no default skills directory exists on a fresh machine) and is silent; any other
// per-skill failure is a diagnostic line, never fatal to the rest of the catalog.
func discoverSkillsUnderRoot(root string, tier Tier, workspaceDir string, diagnostics io.Writer) []skill {
	entries, err := os.ReadDir(root)
	if err != nil {
		if !errors.Is(err, fs.ErrNotExist) {
			fmt.Fprintf(diagnostics, "skills: root %q: %v\n", root, err)
		}
		return nil
	}

	var discovered []skill
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		skillPath := filepath.Join(root, entry.Name(), skillFileName)
		data, err := readInstructionFile(skillPath)
		if err != nil {
			if !errors.Is(err, fs.ErrNotExist) {
				fmt.Fprintf(diagnostics, "skills: %s: %v\n", skillPath, err)
			}
			continue
		}
		parsed, err := parseSkillMarkdown(data)
		if err != nil {
			fmt.Fprintf(diagnostics, "skills: %s: %v\n", skillPath, err)
			continue
		}

		skillTier := tier
		if isWithinWorkspace(skillPath, workspaceDir) {
			skillTier = TierWorkspace
		}
		body := parsed.body
		if skillTier == TierWorkspace {
			body = core.WrapUntrustedContent(fmt.Sprintf("workspace skill:%s", parsed.name), body)
		}
		discovered = append(discovered, skill{
			Name:        parsed.name,
			Description: parsed.description,
			Body:        body,
			Tier:        skillTier,
			Root:        root,
			ContentHash: sha256.Sum256(data),
		})
	}
	return discovered
}

type parsedSkill struct {
	name        string
	description string
	body        string
}

// parseSkillMarkdown splits a SKILL.md into its YAML frontmatter and body. Only name
// and description are read today; DESIGN.md's Skill struct sketches a generic
// Frontmatter bag, but nothing in this ticket's scope consumes any other field.
func parseSkillMarkdown(data []byte) (parsedSkill, error) {
	const delimiter = "---"

	content := strings.TrimLeft(string(data), "\r\n")
	if !strings.HasPrefix(content, delimiter) {
		return parsedSkill{}, errors.New("missing frontmatter: file must start with \"---\"")
	}
	content = strings.TrimPrefix(content, delimiter)

	closeMarker := "\n" + delimiter
	closeIdx := strings.Index(content, closeMarker)
	if closeIdx < 0 {
		return parsedSkill{}, errors.New("missing closing \"---\"")
	}
	frontmatterRaw := content[:closeIdx]
	rest := content[closeIdx+len(closeMarker):]
	if nl := strings.IndexByte(rest, '\n'); nl >= 0 {
		rest = rest[nl+1:]
	} else {
		rest = ""
	}

	var frontmatter struct {
		Name        string `yaml:"name"`
		Description string `yaml:"description"`
	}
	if err := yaml.Unmarshal([]byte(frontmatterRaw), &frontmatter); err != nil {
		return parsedSkill{}, fmt.Errorf("invalid frontmatter: %w", err)
	}
	name := strings.TrimSpace(frontmatter.Name)
	description := strings.TrimSpace(frontmatter.Description)
	if name == "" || description == "" {
		return parsedSkill{}, errors.New("frontmatter must set both name and description")
	}
	if !skillNamePattern.MatchString(name) {
		return parsedSkill{}, fmt.Errorf("skill name %q must match %s", name, skillNamePattern)
	}
	return parsedSkill{name: name, description: description, body: strings.TrimSpace(rest)}, nil
}
