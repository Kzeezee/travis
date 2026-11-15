package broker

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"

	"github.com/pelletier/go-toml/v2"

	"github.com/Kzeezee/travis/internal/core"
)

type DecisionRuleSummary struct {
	ID   string
	Rule core.DecisionRule
}

type SecuritySnapshot struct {
	Grants        []core.Capability
	Denies        []core.Capability
	Rules         []DecisionRuleSummary
	RecentDenials []string
}

func (b *PolicyEnforcedBroker) ReportSecuritySnapshot(ctx context.Context) (SecuritySnapshot, error) {
	if err := ctx.Err(); err != nil {
		return SecuritySnapshot{}, err
	}
	rules, err := b.listDecisionRules()
	if err != nil {
		return SecuritySnapshot{}, err
	}
	grants, denies := b.sessionPolicy.ReportCapabilities()
	return SecuritySnapshot{
		Grants: grants, Denies: denies, Rules: rules, RecentDenials: slices.Clone(b.recentDenials),
	}, nil
}

func (b *PolicyEnforcedBroker) RevokeDecisionRule(ctx context.Context, id string) error {
	if err := validateDecisionRuleID(id); err != nil {
		return err
	}
	removed, err := b.revokeDecisionRule(ctx, id)
	if err != nil {
		return err
	}
	if !removed {
		return fmt.Errorf("no remembered permission %q: %w", id, core.ErrNotFound)
	}
	clear(b.approvals.alwaysAllowed)
	rules, err := b.listDecisionRules()
	if err != nil {
		return err
	}
	for _, summary := range rules {
		rule := summary.Rule
		b.approvals.alwaysAllowed[core.Capability{Kind: rule.Kind, Scope: rule.Scope}] = true
	}
	return nil
}

func validateDecisionRuleID(id string) error {
	decoded, err := hex.DecodeString(id)
	if err != nil || len(decoded) != 6 {
		return fmt.Errorf("invalid permission ID %q: %w", id, core.ErrInvalid)
	}
	return nil
}

func (b *LocalBroker) listDecisionRules() ([]DecisionRuleSummary, error) {
	path := filepath.Join(b.resolvedRoot, filepath.FromSlash(decisionsRelPath))
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("decisions: read: %w", err)
	}
	var file decisionRuleFile
	if err := toml.Unmarshal(data, &file); err != nil {
		return nil, fmt.Errorf("decisions: decode: %w", err)
	}
	rules := make([]DecisionRuleSummary, 0, len(file.Rule))
	for _, rule := range file.Rule {
		if err := validateDecisionRule(rule); err != nil {
			return nil, fmt.Errorf("decisions: %w", err)
		}
		rules = append(rules, DecisionRuleSummary{ID: buildDecisionRuleID(rule), Rule: rule})
	}
	return rules, nil
}

func (b *LocalBroker) revokeDecisionRule(ctx context.Context, id string) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	path := filepath.Join(b.resolvedRoot, filepath.FromSlash(decisionsRelPath))
	if _, err := os.Lstat(path); os.IsNotExist(err) {
		return false, nil
	} else if err != nil {
		return false, fmt.Errorf("decisions: inspect: %w", err)
	}
	file, err := openPrivateFileNoFollow(path)
	if err != nil {
		return false, fmt.Errorf("decisions: %w", err)
	}
	defer file.Close()
	if err := lockExclusive(file); err != nil {
		return false, fmt.Errorf("decisions: lock: %w", err)
	}
	defer func() { _ = unlockExclusive(file) }()
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return false, fmt.Errorf("decisions: seek: %w", err)
	}
	data, err := io.ReadAll(file)
	if err != nil {
		return false, fmt.Errorf("decisions: read: %w", err)
	}
	var current decisionRuleFile
	if len(data) > 0 {
		if err := toml.Unmarshal(data, &current); err != nil {
			return false, fmt.Errorf("decisions: decode: %w", err)
		}
	}
	kept := current.Rule[:0]
	for _, rule := range current.Rule {
		if buildDecisionRuleID(rule) != id {
			kept = append(kept, rule)
		}
	}
	if len(kept) == len(current.Rule) {
		return false, nil
	}
	encoded, err := toml.Marshal(decisionRuleFile{Rule: kept})
	if err != nil {
		return false, fmt.Errorf("decisions: encode: %w", err)
	}
	if err := file.Truncate(0); err != nil {
		return false, fmt.Errorf("decisions: truncate: %w", err)
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return false, fmt.Errorf("decisions: seek: %w", err)
	}
	if _, err := file.Write(encoded); err != nil {
		return false, fmt.Errorf("decisions: write: %w", err)
	}
	return true, file.Sync()
}

func buildDecisionRuleID(rule core.DecisionRule) string {
	encoded, _ := json.Marshal(struct {
		Kind, Scope, Decision, Source string
	}{string(rule.Kind), rule.Scope, string(rule.Decision), rule.Source})
	sum := sha256.Sum256(encoded)
	return fmt.Sprintf("%x", sum[:6])
}
