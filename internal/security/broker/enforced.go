package broker

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

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

// PolicyEnforcedBroker is the broker process's effect performer: every path is checked by the
// session policy, as written and once symlinks are followed, before any IO touches it
// (§5.12.1). Ask is answered with core.ApprovalRequiredError; the client puts it to the human and
// reports back with ApproveOnce or AddDecisionRule before retrying.
//
// Every write is backed up and logged before it happens, and every refusal, ask and approval is
// logged, so the session log is the broker's own record of what it did (§4.4, §5.15).
type PolicyEnforcedBroker struct {
	*LocalBroker
	sessionPolicy policy.Policy
	approvals     approvalLedger
	records       SessionRecords
}

var _ core.Broker = (*PolicyEnforcedBroker)(nil)

func BuildPolicyEnforcedBroker(workspaceDir string, sessionPolicy policy.Policy, records SessionRecords, clock core.Clock) *PolicyEnforcedBroker {
	b := &PolicyEnforcedBroker{
		LocalBroker:   buildLocalBrokerForWorkspace(workspaceDir, clock),
		sessionPolicy: sessionPolicy,
		approvals: approvalLedger{
			alwaysAllowed: map[core.Capability]bool{}, onceAllowed: map[core.Capability]bool{},
			irreversibleAsked: map[core.Capability]bool{}, irreversibleAllowed: map[core.Capability]bool{},
		},
		records: records,
	}
	b.paths = config.PathSet{WorkspaceDir: b.resolvedRoot}
	b.authorizeRead = func(ctx context.Context, path string) error { return b.authorize(ctx, core.CapabilityFSRead, path) }
	b.authorizeWrite = func(ctx context.Context, path string) error { return b.authorize(ctx, core.CapabilityFSWrite, path) }
	b.recordWrite = b.backUpAndLogWrite
	return b
}

func (b *PolicyEnforcedBroker) DescribePath(ctx context.Context, path string) (core.PathInfo, error) {
	info, err := b.LocalBroker.DescribePath(ctx, path)
	b.approvals.settleOnce(err)
	return info, err
}

func (b *PolicyEnforcedBroker) ListDirectory(ctx context.Context, path string) ([]core.PathInfo, error) {
	entries, err := b.LocalBroker.ListDirectory(ctx, path)
	b.approvals.settleOnce(err)
	return entries, err
}

func (b *PolicyEnforcedBroker) OpenFile(ctx context.Context, path string) (core.OpenedFile, error) {
	opened, err := b.LocalBroker.OpenFile(ctx, path)
	b.approvals.settleOnce(err)
	return opened, err
}

func (b *PolicyEnforcedBroker) WriteFile(ctx context.Context, path string, content []byte) (core.PathInfo, error) {
	info, err := b.LocalBroker.WriteFile(ctx, path, content)
	b.approvals.settleOnce(err)
	return info, err
}

// ApproveOnce lets the next effect through an Ask on exactly this capability, once. Only an
// Ask can be approved: a target policy denies or never granted stays refused (§6.4). ctx must
// carry the same untrusted-step mark as the request that asked, since that mark is why it asked.
// An irreversible write the broker asked about can likewise be approved once, and only once.
func (b *PolicyEnforcedBroker) ApproveOnce(ctx context.Context, approved core.Capability) error {
	if b.approvals.irreversibleAsked[approved] {
		if err := b.appendBrokerFact(ctx, core.SessionEntry{Kind: core.EntryApprovalGiven, Capability: &approved, IsIrreversible: true}); err != nil {
			return err
		}
		delete(b.approvals.irreversibleAsked, approved)
		b.approvals.irreversibleAllowed[approved] = true
		return nil
	}
	if err := b.requireAsk(ctx, approved); err != nil {
		return err
	}
	if err := b.appendBrokerFact(ctx, core.SessionEntry{Kind: core.EntryApprovalGiven, Capability: &approved}); err != nil {
		return err
	}
	b.approvals.onceAllowed[approved] = true
	return nil
}

// AddDecisionRule persists the rule (D-30) and honours it for the rest of the session. Like
// ApproveOnce it can only narrow a policy Ask, never widen a Deny; an irreversible write is not
// a policy Ask, so it can never become an always rule (§6.4).
func (b *PolicyEnforcedBroker) AddDecisionRule(ctx context.Context, rule core.DecisionRule) error {
	if err := validateDecisionRule(rule); err != nil {
		return err
	}
	if core.ReportIsFromUntrustedStep(ctx) {
		return fmt.Errorf("an untrusted step cannot create an always rule: %w", core.ErrDenied)
	}
	approved := core.Capability{Kind: rule.Kind, Scope: rule.Scope}
	if err := b.requireAsk(ctx, approved); err != nil {
		return err
	}
	if err := b.appendBrokerFact(ctx, core.SessionEntry{Kind: core.EntryApprovalGiven, Capability: &approved, IsAlways: true}); err != nil {
		return err
	}
	if err := b.LocalBroker.AddDecisionRule(ctx, rule); err != nil {
		return err
	}
	b.approvals.alwaysAllowed[approved] = true
	return nil
}

func (b *PolicyEnforcedBroker) requireAsk(ctx context.Context, approved core.Capability) error {
	decision := b.sessionPolicy.Evaluate(ctx, buildPolicyRequest(ctx, approved.Kind, approved.Scope))
	if decision.Verdict != policy.Ask {
		return fmt.Errorf("%s %q is not awaiting approval (%s): %w", approved.Kind, approved.Scope, reportDecidingRule(decision), core.ErrDenied)
	}
	return nil
}

func (b *PolicyEnforcedBroker) authorize(ctx context.Context, kind core.CapabilityKind, path string) error {
	decision := b.sessionPolicy.Evaluate(ctx, buildPolicyRequest(ctx, kind, path))
	switch decision.Verdict {
	case policy.Allow:
		return nil
	case policy.Ask:
		asked := core.Capability{Kind: kind, Scope: path}
		if b.approvals.isAllowed(asked, core.ReportIsFromUntrustedStep(ctx)) {
			return nil
		}
		explanation := strings.Join(decision.Trace, "\n")
		required := &core.ApprovalRequiredError{Operation: core.Operation{Kind: kind, Target: path, Explanation: explanation}}
		if err := b.appendBrokerFact(ctx, core.SessionEntry{Kind: core.EntryApprovalAsked, Capability: &asked, Detail: explanation}); err != nil {
			return err
		}
		return required
	case policy.Deny:
		rule := reportDecidingRule(decision)
		denied := core.Capability{Kind: kind, Scope: path}
		refusal := fmt.Errorf("refused by policy (%s): %w", rule, core.ErrDenied)
		return errors.Join(refusal, b.appendBrokerFact(ctx, core.SessionEntry{Kind: core.EntryPolicyDenied, Capability: &denied, Detail: rule}))
	}
	return fmt.Errorf("unknown verdict %v for %s %q: %w", decision.Verdict, kind, path, core.ErrDenied)
}

// buildPolicyRequest is one effect as policy sees it, including whether its step is untrusted.
func buildPolicyRequest(ctx context.Context, kind core.CapabilityKind, target string) policy.Request {
	return policy.Request{Kind: kind, Target: target, IsFromUntrustedStep: core.ReportIsFromUntrustedStep(ctx)}
}

func reportDecidingRule(decision policy.Decision) string {
	if len(decision.Trace) == 0 {
		return "no rule recorded"
	}
	return decision.Trace[len(decision.Trace)-1]
}

// approvalLedger holds the human's answers for this session. Once-approvals accumulate while
// one effect still needs more of them (its as-written and resolved paths can each Ask, and a
// write can then turn out irreversible), and are spent as soon as that effect finishes any other
// way; one the user abandons mid-effect lingers, for that exact target only, until the next
// effect finishes. It has no lock: the broker's one connection is served one request at a time.
type approvalLedger struct {
	alwaysAllowed map[core.Capability]bool
	onceAllowed   map[core.Capability]bool
	// irreversibleAsked are writes the broker asked about because it could not back them up;
	// irreversibleAllowed are the ones the human then let through once. Kept apart from the
	// policy answers, so approving a policy Ask never also waives the irreversibility question.
	irreversibleAsked   map[core.Capability]bool
	irreversibleAllowed map[core.Capability]bool
}

func (l *approvalLedger) isAllowed(c core.Capability, isFromUntrustedStep bool) bool {
	return l.onceAllowed[c] || (!isFromUntrustedStep && l.alwaysAllowed[c])
}

func (l *approvalLedger) settleOnce(effectErr error) {
	if !errors.As(effectErr, new(*core.ApprovalRequiredError)) {
		clear(l.onceAllowed)
		clear(l.irreversibleAsked)
		clear(l.irreversibleAllowed)
	}
}
