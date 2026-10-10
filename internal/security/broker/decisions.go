package broker

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/pelletier/go-toml/v2"

	"github.com/Kzeezee/travis/internal/core"
)

// decisionsRelPath is where durable "always allow" rules live; it is write-denied to
// fs_write (D-30, R-26), so AddDecisionRule is the only way anything can reach it.
const decisionsRelPath = ".travis/decisions.toml"

const (
	decisionsDirMode  = 0o700
	decisionsFileMode = 0o600
)

// decisionRuleFile is one append-worthy TOML document: a single [[rule]] table, so
// repeated appends accumulate into a valid array of tables.
type decisionRuleFile struct {
	Rule []core.DecisionRule `toml:"rule"`
}

// AddDecisionRule appends rule to decisions.toml under an exclusive lock (D-32), bypassing
// resolveInsideWorkspace's deny-glob check on purpose: that check exists to stop fs_write
// from reaching .travis/**, and this method is the one sanctioned way around it, not a
// weakening of it.
func (b *LocalBroker) AddDecisionRule(ctx context.Context, rule core.DecisionRule) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := validateDecisionRule(rule); err != nil {
		return err
	}
	if rule.AddedAt.IsZero() {
		rule.AddedAt = time.Now().UTC()
	}

	path := filepath.Join(b.root, filepath.FromSlash(decisionsRelPath))
	if err := os.MkdirAll(filepath.Dir(path), decisionsDirMode); err != nil {
		return fmt.Errorf("decisions: %w", err)
	}
	// Opened read-write rather than O_APPEND: on Windows, O_APPEND maps to the
	// FILE_APPEND_DATA access right, which LockFileEx refuses with ACCESS_DENIED. Locking
	// first and seeking to end afterward gets the same "append under lock" guarantee on
	// every OS without relying on a platform-specific append mode.
	file, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, decisionsFileMode)
	if err != nil {
		return fmt.Errorf("decisions: open: %w", err)
	}
	defer file.Close()

	if err := lockExclusive(file); err != nil {
		return fmt.Errorf("decisions: lock: %w", err)
	}
	defer unlockExclusive(file)

	if _, err := file.Seek(0, io.SeekEnd); err != nil {
		return fmt.Errorf("decisions: seek: %w", err)
	}
	encoded, err := toml.Marshal(decisionRuleFile{Rule: []core.DecisionRule{rule}})
	if err != nil {
		return fmt.Errorf("decisions: encode: %w", err)
	}
	if _, err := file.Write(encoded); err != nil {
		return fmt.Errorf("decisions: write: %w", err)
	}
	return file.Sync()
}

// validateDecisionRule enforces the two guarantees D-30 and D-27 require before anything
// is written: exec can never become a rule, and a rule must name a real kind/scope and
// the one supported outcome.
func validateDecisionRule(rule core.DecisionRule) error {
	switch {
	case rule.Kind == core.CapabilityExec:
		return fmt.Errorf("exec can never become a decision rule (D-27): %w", core.ErrDenied)
	case rule.Kind == "" || rule.Scope == "":
		return fmt.Errorf("decision rule needs a kind and scope: %w", core.ErrInvalid)
	case rule.Decision != core.DecisionAllow:
		return fmt.Errorf("unsupported decision %q: %w", rule.Decision, core.ErrInvalid)
	}
	return nil
}
