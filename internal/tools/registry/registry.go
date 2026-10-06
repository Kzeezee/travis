// Package registry is the single place built-in, MCP, skill and subagent tools are found (DESIGN §5.5).
// Flow: BuildRegistry → Register (startup, each tool, collisions rejected) →
// Prune (each loop step, tools offered to the model) → Lookup (model called a tool) → Tool.Invoke

package registry

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"sync"

	"github.com/Kzeezee/travis/internal/core"
)

type Source string

const (
	SourceBuiltin Source = "core"
	SourceMCP     Source = "mcp"
	SourceSkill   Source = "skill"
	SourceSwarm   Source = "swarm"
)

// toolNamePattern is the strictest function-name rule among providers (D-31), so a
// registered name reaches every model unchanged and no adapter has to translate it.
var toolNamePattern = regexp.MustCompile(`^[a-zA-Z0-9_-]{1,64}$`)

type Filter struct {
	Source     Source
	NamePrefix string
}

type registration struct {
	source Source
	tool   core.Tool
}

type Registry struct {
	mu         sync.RWMutex
	byName     map[string]registration
	maxExposed int
}

func BuildRegistry(maxExposed int) *Registry {
	return &Registry{
		byName:     make(map[string]registration),
		maxExposed: maxExposed,
	}
}

// add tools to registry
func (r *Registry) Register(source Source, tool core.Tool) error {
	name := tool.Name()
	if !toolNamePattern.MatchString(name) {
		return fmt.Errorf("register tool %q from source %q: name must match %s (D-31)", name, source, toolNamePattern)
	}
	if specName := tool.Describe().Name; specName != name {
		return fmt.Errorf("register tool %q from source %q: Describe().Name is %q", name, source, specName)
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	if existing, isTaken := r.byName[name]; isTaken {
		return fmt.Errorf("register tool %q from source %q: collides with existing registration from source %q",
			name, source, existing.source)
	}
	r.byName[name] = registration{source: source, tool: tool}
	return nil
}

// get tool by name
func (r *Registry) Lookup(name string) (core.Tool, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	reg, isFound := r.byName[name]
	return reg.tool, isFound
}

// get sorted tools filtered by source and name
func (r *Registry) List(filter Filter) []core.Tool {
	r.mu.RLock()
	defer r.mu.RUnlock()

	tools := make([]core.Tool, 0, len(r.byName))
	for name, reg := range r.byName {
		if filter.Source != "" && reg.source != filter.Source {
			continue
		}
		if !strings.HasPrefix(name, filter.NamePrefix) {
			continue
		}
		tools = append(tools, reg.tool)
	}
	sort.Slice(tools, func(i, j int) bool { return tools[i].Name() < tools[j].Name() })
	return tools
}

// decide which tools to offer to the model, placeholder until Phase 3, D-03
func (r *Registry) Prune(_ context.Context, budget int, _ string) []core.Tool {
	tools := r.List(Filter{})
	limit := r.maxExposed
	if budget > 0 && budget < limit {
		limit = budget
	}
	if len(tools) > limit {
		tools = tools[:limit]
	}
	return tools
}
