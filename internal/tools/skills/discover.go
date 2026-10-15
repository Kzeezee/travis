package skills

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/Kzeezee/travis/internal/config"
	"github.com/Kzeezee/travis/internal/core"
)

const skillFileName = "SKILL.md"

// discoverSkills resolves every configured root, classifies its tier by workspace
// containment, and walks it for skills, reporting each tier separately.
func discoverSkills(cfg config.SkillsCfg, paths config.PathSet, diagnostics io.Writer) (workspaceSkills, userSkills []skill) {
	for _, root := range cfg.Roots {
		resolved, err := paths.Resolve(root)
		if err != nil {
			fmt.Fprintf(diagnostics, "skills: root %q: %v\n", root, err)
			continue
		}
		if isWithinWorkspace(resolved, paths.WorkspaceDir) {
			workspaceSkills = append(workspaceSkills, discoverSkillsUnderRoot(resolved, TierWorkspace, diagnostics)...)
		} else {
			userSkills = append(userSkills, discoverSkillsUnderRoot(resolved, TierUser, diagnostics)...)
		}
	}
	return workspaceSkills, userSkills
}

// isWithinWorkspace decides trust tier by where a root actually lives, not by which
// config.PathSet.Resolve prefix produced it (R-02 is about workspace-owned content,
// regardless of how the path was spelled).
func isWithinWorkspace(path, workspaceDir string) bool {
	rel, err := filepath.Rel(workspaceDir, path)
	if err != nil {
		return false
	}
	return rel == "." || (!strings.HasPrefix(rel, "..") && !filepath.IsAbs(rel))
}

// discoverSkillsUnderRoot walks one directory level: root/<skill-name>/SKILL.md,
// matching the real SKILL.md ecosystem convention. A missing root is the common case
// (no default skills directory exists on a fresh machine) and is silent; any other
// per-skill failure is a diagnostic line, never fatal to the rest of the catalog.
func discoverSkillsUnderRoot(root string, tier Tier, diagnostics io.Writer) []skill {
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
		data, err := os.ReadFile(skillPath)
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

		body := parsed.body
		if tier == TierWorkspace {
			body = core.WrapUntrustedContent(fmt.Sprintf("workspace skill:%s", parsed.name), body)
		}
		discovered = append(discovered, skill{
			Name:        parsed.name,
			Description: parsed.description,
			Body:        body,
			Tier:        tier,
			Root:        root,
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
	return parsedSkill{name: name, description: description, body: strings.TrimSpace(rest)}, nil
}
