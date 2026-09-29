// Package config additionally owns the layered Config that every other module reads
// its settings from (DESIGN.md 5.2): compiled defaults, system file, user file,
// workspace file, caller-supplied extra files, then TRAVIS_* environment variables.
package config

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/pelletier/go-toml/v2"

	"github.com/Kzeezee/travis/internal/core"
)

// Config is the fully merged, validated settings every subsystem reads from. Fields
// are added by whichever ticket first needs that subsystem's config, not all at once.
type Config struct {
	Profile  string
	Provider ProviderCfg
	Brain    BrainCfg
	Tools    ToolsCfg
}

// ProviderCfg selects which model profile is live and lists every profile a user has
// defined, keyed by whatever name they chose.
type ProviderCfg struct {
	Active   string
	Profiles map[string]ProviderProfileCfg
}

type ProviderProfileCfg struct {
	Kind      string
	BaseURL   string `toml:"base_url"`
	Model     string
	MaxTokens int `toml:"max_tokens"`
	Auth      AuthCfg
}

// AuthCfg never carries a credential value, only a reference the broker resolves
// later (`keyring://`, `env://`, `cmd://`) — see ResolveProviderAPIKey.
//
// Known deviation, accepted for Phase 1: Ref is decoded but not yet consulted.
// ResolveProviderAPIKey always reads OPENAI_API_KEY regardless of what Ref names, so
// every profile shares one credential source until the broker (which resolves Ref
// for real) arrives.
type AuthCfg struct {
	Kind string
	Ref  string
}

// BrainCfg bounds one assistant turn (DESIGN.md 5.4).
type BrainCfg struct {
	MaxSteps       int     `toml:"max_steps"`
	MaxTurnCostUSD float64 `toml:"max_turn_cost_usd"`
	MaxTurnTokens  int     `toml:"max_turn_tokens"`
}

// ToolsCfg bounds what the registry exposes to the model each step (DESIGN.md 5.5).
// Per-tool approval overrides ([tools.approval]) are deferred: they resolve against
// `security`, which doesn't exist yet.
type ToolsCfg struct {
	ApprovalDefault string `toml:"approval_default"`
	MaxExposed      int    `toml:"max_exposed"`
}

// DiagnosticSeverity distinguishes a warning a user can ignore from a hard failure.
type DiagnosticSeverity int

const (
	SeverityWarning DiagnosticSeverity = iota
	SeverityError
)

type Diagnostic struct {
	Severity DiagnosticSeverity
	Message  string
}

type Diagnostics []Diagnostic

func (d Diagnostics) HasErrors() bool {
	for _, diag := range d {
		if diag.Severity == SeverityError {
			return true
		}
	}
	return false
}

// hardErrorSection is the one section where an unknown key is never just a typo
// warning: a misspelled security setting must never silently widen what the agent
// is allowed to do. Every key under here is "unknown" until `security` ships its own
// Config field, which is the correct failure mode for now, not a gap.
const hardErrorSection = "security"

const (
	envProviderActive  = "TRAVIS_PROVIDER_ACTIVE"
	envBrainMaxSteps   = "TRAVIS_BRAIN_MAX_STEPS"
	envToolsMaxExposed = "TRAVIS_TOOLS_MAX_EXPOSED"
)

// fileReader abstracts file reads so tests can supply in-memory layers instead of a
// real filesystem.
type fileReader func(name string) ([]byte, error)

// Load merges every config source into one Config, lowest precedence first: compiled
// defaults, system config, user config, workspace config, then any extra file paths
// the caller supplies (e.g. a --config flag), then TRAVIS_* environment variables.
// CLI flags are the highest layer in DESIGN.md 5.2, but are applied by the
// composition root after Load returns — config must not depend on any particular
// flag set.
func Load(paths ...string) (*Config, Diagnostics, error) {
	pathSet, err := BuildCurrentPathSet()
	if err != nil {
		return nil, nil, err
	}
	return load(os.ReadFile, pathSet, paths, os.LookupEnv)
}

func load(readFile fileReader, pathSet PathSet, extraPaths []string, lookup envLookup) (*Config, Diagnostics, error) {
	cfg := buildDefaultConfig()
	var diags Diagnostics

	userConfigFile := filepath.Join(pathSet.ConfigDir, "config.toml")
	layers := append([]string{pathSet.SystemConfigFile, userConfigFile, pathSet.WorkspaceConfigFile}, extraPaths...)
	for _, layerPath := range layers {
		layerDiags, err := decodeLayerFile(readFile, layerPath, &cfg)
		diags = append(diags, layerDiags...)
		if err != nil {
			return nil, diags, err
		}
	}

	applyEnvOverrides(&cfg, lookup)

	diags = append(diags, cfg.Validate()...)
	if diags.HasErrors() {
		return nil, diags, fmt.Errorf("config: invalid configuration: %w", core.ErrInvalid)
	}
	return &cfg, diags, nil
}

// buildDefaultConfig is the bottom layer of the precedence chain: Travis's compiled
// defaults, applied before any file is read.
func buildDefaultConfig() Config {
	return Config{
		Provider: ProviderCfg{
			Active: "cloud",
			Profiles: map[string]ProviderProfileCfg{
				"cloud": {
					Kind:    "openai_compatible",
					BaseURL: "https://api.openai.com/v1",
					Model:   "gpt-4o-mini",
				},
			},
		},
		Brain: BrainCfg{MaxSteps: 40},
		Tools: ToolsCfg{ApprovalDefault: "ask", MaxExposed: 60},
	}
}

// decodeLayerFile merges one TOML file into cfg. A missing file is a normal, silent
// no-op — most layers (system config, workspace config) usually don't exist.
func decodeLayerFile(readFile fileReader, path string, cfg *Config) (Diagnostics, error) {
	if path == "" {
		return nil, nil
	}
	data, err := readFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("config %s: %w", path, err)
	}

	diags, err := reportUnknownKeys(path, data)
	if err != nil {
		return diags, err
	}

	if err := toml.Unmarshal(data, cfg); err != nil {
		return diags, fmt.Errorf("config %s: %w", path, err)
	}
	return diags, nil
}

// reportUnknownKeys decodes data in strict mode purely to find keys with no matching
// Config field. It never keeps that decode's values — toml.Unmarshal in
// decodeLayerFile is what actually applies the file, leniently, so an unrelated typo
// elsewhere in the file doesn't stop the rest of it from loading.
func reportUnknownKeys(path string, data []byte) (Diagnostics, error) {
	var diags Diagnostics

	var probe Config
	strictErr := toml.NewDecoder(bytes.NewReader(data)).DisallowUnknownFields().Decode(&probe)
	if strictErr == nil {
		return diags, nil
	}

	var missing *toml.StrictMissingError
	if !errors.As(strictErr, &missing) {
		return diags, fmt.Errorf("config %s: %w", path, strictErr)
	}

	for _, decodeErr := range missing.Errors {
		key := decodeErr.Key()
		dotted := strings.Join(key, ".")
		if len(key) > 0 && strings.EqualFold(key[0], hardErrorSection) {
			return diags, fmt.Errorf("config %s: unknown key %q under [%s]: %w", path, dotted, hardErrorSection, core.ErrInvalid)
		}
		diags = append(diags, Diagnostic{
			Severity: SeverityWarning,
			Message:  fmt.Sprintf("config %s: unknown key %q", path, dotted),
		})
	}
	return diags, nil
}

// applyEnvOverrides is the highest file-free layer. Only unambiguous scalars are
// supported here — overriding one field of a specific named profile would need a
// dynamic env var per profile name, which DESIGN.md's TRAVIS_* convention does not
// define, so per-profile changes still go through the TOML file.
func applyEnvOverrides(cfg *Config, lookup envLookup) {
	if v := readTrimmedEnvValue(lookup, envProviderActive); v != "" {
		cfg.Provider.Active = v
	}
	if v := readTrimmedEnvValue(lookup, envBrainMaxSteps); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			cfg.Brain.MaxSteps = n
		}
	}
	if v := readTrimmedEnvValue(lookup, envToolsMaxExposed); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			cfg.Tools.MaxExposed = n
		}
	}
}

// Validate reports problems Load's decode step cannot catch on its own. It never
// panics; callers decide whether any Diagnostic.Severity == SeverityError should be
// fatal (Load already treats it as such).
func (c *Config) Validate() Diagnostics {
	var diags Diagnostics

	if c.Provider.Active == "" {
		diags = append(diags, Diagnostic{Severity: SeverityError, Message: "provider.active is empty"})
	} else if _, ok := c.Provider.Profiles[c.Provider.Active]; !ok {
		diags = append(diags, Diagnostic{
			Severity: SeverityError,
			Message:  fmt.Sprintf("provider.active %q has no matching [provider.profiles.%s]", c.Provider.Active, c.Provider.Active),
		})
	}
	for name, profile := range c.Provider.Profiles {
		if profile.BaseURL == "" {
			diags = append(diags, Diagnostic{Severity: SeverityError, Message: fmt.Sprintf("provider.profiles.%s.base_url is empty", name)})
		}
		if profile.Model == "" {
			diags = append(diags, Diagnostic{Severity: SeverityError, Message: fmt.Sprintf("provider.profiles.%s.model is empty", name)})
		}
	}

	if c.Brain.MaxSteps <= 0 {
		diags = append(diags, Diagnostic{Severity: SeverityError, Message: "brain.max_steps must be positive"})
	}

	if c.Tools.MaxExposed <= 0 {
		diags = append(diags, Diagnostic{Severity: SeverityError, Message: "tools.max_exposed must be positive"})
	}
	switch c.Tools.ApprovalDefault {
	case "auto", "ask", "never":
	default:
		diags = append(diags, Diagnostic{
			Severity: SeverityWarning,
			Message:  fmt.Sprintf("tools.approval_default %q is not one of auto, ask, never", c.Tools.ApprovalDefault),
		})
	}

	return diags
}
