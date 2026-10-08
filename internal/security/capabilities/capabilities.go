// Package capabilities builds a session's allow-grants and owns the single glob matcher.
package capabilities

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/bmatcuk/doublestar/v4"

	"github.com/Kzeezee/travis/internal/core"
)

type Set []core.Capability

// Roots are the absolute directories and config allowlists a profile is built from.
type Roots struct {
	Workspace, Temp, Home string
	ExecAllow, HostAllow  []string
}

// BuildProfileSet returns the permission grants for a named profile; yolo deferred.
// paranoid: read workspace
// default: read+write workspace, exec ExecAllow, http HostAllow
// dev: read home, write workspace+temp, exec and http anything
func BuildProfileSet(name string, r Roots) (Set, error) {
	switch name {
	case "paranoid":
		return buildRootedSet(core.CapabilityFSRead, r.Workspace, "workspace")
	case "default":
		set, err := buildRootedSet(core.CapabilityFSRead, r.Workspace, "workspace")
		if err != nil {
			return nil, err
		}
		writes, _ := buildRootedSet(core.CapabilityFSWrite, r.Workspace, "workspace")
		set = append(set, writes...)
		if set, err = appendPatterns(set, core.CapabilityExec, r.ExecAllow); err != nil {
			return nil, err
		}
		return appendPatterns(set, core.CapabilityNetworkHTTP, r.HostAllow)
	case "dev":
		set, err := buildRootedSet(core.CapabilityFSRead, r.Home, "home")
		if err != nil {
			return nil, err
		}
		for _, root := range []struct{ path, name string }{{r.Workspace, "workspace"}, {r.Temp, "temp"}} {
			writes, err := buildRootedSet(core.CapabilityFSWrite, root.path, root.name)
			if err != nil {
				return nil, err
			}
			set = append(set, writes...)
		}
		return append(set,
			core.Capability{Kind: core.CapabilityExec, Scope: "*"},
			core.Capability{Kind: core.CapabilityNetworkHTTP, Scope: "*"},
		), nil
	default:
		return nil, fmt.Errorf("unknown security profile %q: %w", name, core.ErrInvalid)
	}
}

// SelectMatching returns the first capability which matches the kind and target
func (s Set) SelectMatching(kind core.CapabilityKind, target string, isCaseInsensitive bool) (core.Capability, bool) {
	if kind == core.CapabilitySecret {
		for _, grant := range s {
			if grant.Kind == kind && grant.Scope == target {
				return grant, true
			}
		}
		return core.Capability{}, false
	}

	target = filepath.ToSlash(target)
	isCaseInsensitive = isCaseInsensitive || kind == core.CapabilityNetworkHTTP
	if isCaseInsensitive {
		target = strings.ToLower(target)
	}
	for _, grant := range s {
		if grant.Kind != kind {
			continue
		}
		scope := grant.Scope
		if isCaseInsensitive {
			scope = strings.ToLower(scope)
		}
		if doublestar.MatchUnvalidated(scope, target) {
			return grant, true
		}
	}
	return core.Capability{}, false
}

func buildRootedSet(kind core.CapabilityKind, root, rootName string) (Set, error) {
	if root == "" || !filepath.IsAbs(root) {
		return nil, fmt.Errorf("%s root %q is not absolute: %w", rootName, root, core.ErrInvalid)
	}
	return Set{{Kind: kind, Scope: buildRootedGlob(root, "**")}}, nil
}

func appendPatterns(set Set, kind core.CapabilityKind, patterns []string) (Set, error) {
	for _, pattern := range patterns {
		pattern = strings.TrimSpace(pattern)
		if kind == core.CapabilityNetworkHTTP {
			pattern = strings.ToLower(pattern)
		}
		if pattern == "" || !doublestar.ValidatePattern(pattern) {
			return nil, fmt.Errorf("invalid %s pattern %q: %w", kind, pattern, core.ErrInvalid)
		}
		set = append(set, core.Capability{Kind: kind, Scope: pattern})
	}
	return set, nil
}

func buildRootedGlob(root, relGlob string) string {
	escapedRoot := globMetaEscaper.Replace(filepath.ToSlash(filepath.Clean(root)))
	return strings.TrimSuffix(escapedRoot, "/") + "/" + relGlob
}

var globMetaEscaper = strings.NewReplacer(`\`, `\\`, `*`, `\*`, `?`, `\?`, `[`, `\[`, `]`, `\]`, `{`, `\{`, `}`, `\}`)
