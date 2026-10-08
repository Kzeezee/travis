package broker

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/Kzeezee/travis/internal/config"
	"github.com/Kzeezee/travis/internal/core"
	"github.com/Kzeezee/travis/internal/security/capabilities"
	"github.com/Kzeezee/travis/internal/security/policy"
)

// SessionRoots are the directories a session's grants and deny-list are anchored to.
type SessionRoots struct {
	Paths config.PathSet
	Home  string
	Temp  string
}

// BuildSessionPolicy builds the policy a broker enforces for one session under the named
// security profile. Roots are symlink-resolved first, because the broker evaluates resolved
// paths and a grant on /var would otherwise never match its own files under /private/var.
func BuildSessionPolicy(profile string, roots SessionRoots) (policy.Policy, error) {
	home := resolveRootSymlinks(roots.Home)
	paths := roots.Paths
	paths.WorkspaceDir = resolveRootSymlinks(paths.WorkspaceDir)
	paths.StateDir = resolveRootSymlinks(paths.StateDir)

	grants, err := capabilities.BuildProfileSet(profile, capabilities.Roots{
		Workspace: paths.WorkspaceDir,
		Temp:      resolveRootSymlinks(roots.Temp),
		Home:      home,
	})
	if err != nil {
		return policy.Policy{}, err
	}
	deny := config.BuildDenyGlobs(paths, home)
	return policy.New(grants, deny.Read, deny.Write, config.IsPathCaseInsensitive()), nil
}

// resolveRootSymlinks keeps a root that does not exist yet as written; nothing under it exists either.
func resolveRootSymlinks(root string) string {
	if resolved, err := filepath.EvalSymlinks(root); err == nil {
		return resolved
	}
	return filepath.Clean(root)
}

// BuildPolicyEnforcedBroker is the broker process's effect performer: every path is checked by
// sessionPolicy, as written and once symlinks are followed, before any IO touches it (§5.12.1).
// Ask is refused until approvals exist (2D), which keeps the broker fail-closed.
func BuildPolicyEnforcedBroker(workspaceDir string, sessionPolicy policy.Policy) *LocalBroker {
	b := buildLocalBrokerForWorkspace(workspaceDir)
	b.paths = config.PathSet{WorkspaceDir: b.resolvedRoot}
	b.authorizeRead = func(path string) error {
		return authorizeByPolicy(sessionPolicy, policy.Request{Kind: core.CapabilityFSRead, Target: path})
	}
	return b
}

func authorizeByPolicy(sessionPolicy policy.Policy, req policy.Request) error {
	decision := sessionPolicy.Evaluate(context.Background(), req)
	switch decision.Verdict {
	case policy.Allow:
		return nil
	case policy.Ask:
		return fmt.Errorf("needs approval, which this broker cannot ask for yet (%s): %w", reportDecidingRule(decision), core.ErrDenied)
	case policy.Deny:
		return fmt.Errorf("refused by policy (%s): %w", reportDecidingRule(decision), core.ErrDenied)
	}
	return fmt.Errorf("unknown verdict %v for %s %q: %w", decision.Verdict, req.Kind, req.Target, core.ErrDenied)
}

func reportDecidingRule(decision policy.Decision) string {
	if len(decision.Trace) == 0 {
		return "no rule recorded"
	}
	return decision.Trace[len(decision.Trace)-1]
}
