// Package policy is the pure allow/ask/deny decision core; it does no IO.
package policy

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/Kzeezee/travis/internal/core"
	"github.com/Kzeezee/travis/internal/security/capabilities"
)

type Verdict int

const (
	Deny Verdict = iota
	Ask
	Allow
)

func (v Verdict) String() string {
	switch v {
	case Allow:
		return "allow"
	case Ask:
		return "ask"
	case Deny:
		return "deny"
	default:
		return "deny"
	}
}

// Request is an already-resolved effect; fs targets must be absolute, clean, post-symlink paths.
type Request struct {
	Kind                core.CapabilityKind
	Target              string
	IsFromUntrustedStep bool
	IsManagedMemory     bool
}

type Decision struct {
	Verdict Verdict
	Trace   []string
}

// Policy is a concrete value, not an interface: there is one implementation.
type Policy struct {
	grants            capabilities.Set
	deny              capabilities.Set
	isCaseInsensitive bool
	managedMemoryPath string
}

// New builds a policy; callers extend readDeny (e.g. from .travisignore), never shrink it.
func New(grants capabilities.Set, readDeny, writeDeny []string, isCaseInsensitive bool, managedMemoryPath string) Policy {
	var deny capabilities.Set
	for _, glob := range readDeny {
		deny = append(deny, core.Capability{Kind: core.CapabilityFSRead, Scope: glob})
	}
	for _, glob := range writeDeny {
		deny = append(deny, core.Capability{Kind: core.CapabilityFSWrite, Scope: glob})
	}
	return Policy{grants: grants, deny: deny, isCaseInsensitive: isCaseInsensitive, managedMemoryPath: managedMemoryPath}
}

// Checks if request is in deny list -> check if it is in grants -> asks if running a command/untrusted input
func (p Policy) Evaluate(_ context.Context, req Request) Decision {
	var trace []string
	decide := func(v Verdict, format string, args ...any) Decision {
		trace = append(trace, v.String()+": "+fmt.Sprintf(format, args...))
		return Decision{Verdict: v, Trace: trace}
	}
	note := func(format string, args ...any) { trace = append(trace, "pass: "+fmt.Sprintf(format, args...)) }

	isFS := req.Kind == core.CapabilityFSRead || req.Kind == core.CapabilityFSWrite
	if req.IsManagedMemory && (!isFS || req.Target != p.managedMemoryPath || p.managedMemoryPath == "") {
		return decide(Deny, "managed memory request does not name the broker's memory file")
	}
	if isFS {
		// Symlink resolution is the broker's job; here an unresolved-looking path is simply refused.
		if !isResolvedPath(req.Target) {
			return decide(Deny, "%s %q is not a resolved absolute path", req.Kind, req.Target)
		}
		note("%s %q is resolved", req.Kind, req.Target)
		if !req.IsManagedMemory {
			if glob, isDenied := p.deny.SelectMatching(req.Kind, req.Target, p.isCaseInsensitive); isDenied {
				return decide(Deny, "%s %q matches deny %q", req.Kind, req.Target, glob.Scope)
			}
		}
		note("%s %q matches no deny rule", req.Kind, req.Target)
	}

	grant, isGranted := p.grants.SelectMatching(req.Kind, req.Target, p.isCaseInsensitive)
	if !isGranted {
		return decide(Deny, "no %s grant covers %q", req.Kind, req.Target)
	}
	note("%s %q granted by %q", req.Kind, req.Target, grant.Scope)

	if req.Kind == core.CapabilityExec {
		return decide(Ask, "exec is irreversible and always asks (D-27)")
	}
	if req.IsFromUntrustedStep {
		return decide(Ask, "request originates in a step with untrusted intake (§5.12.4)")
	}
	return decide(Allow, "%s %q", req.Kind, req.Target)
}

func (p Policy) Explain(req Request) string {
	return strings.Join(p.Evaluate(context.Background(), req).Trace, "\n")
}

func isResolvedPath(target string) bool {
	slashTarget := filepath.ToSlash(target)
	return filepath.IsAbs(target) &&
		filepath.Clean(target) == target &&
		!strings.HasPrefix(slashTarget, "//?/") &&
		!strings.HasPrefix(slashTarget, "//./")
}
