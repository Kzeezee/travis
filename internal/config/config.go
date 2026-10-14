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
	Eval     EvalCfg
	Session  SessionCfg
	MCP      MCPCfg
	Skills   SkillsCfg
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

// MCPCfg lists every local MCP server a user has defined, keyed by whatever name they
// chose. User-config only — see workspaceLayerCfg's doc comment: a cloned repo must never
// be able to make `travis chat` exec a command just by being `cd`'d into (DESIGN.md R-27,
// here for code execution rather than credential redirection).
type MCPCfg struct {
	Servers map[string]MCPServerCfg
}

type MCPServerCfg struct {
	Command string
	Args    []string
	Env     map[string]string
}

// SkillsCfg configures the SKILL.md loader (DESIGN.md §5.8). User-config only, same
// reasoning as MCPCfg (DESIGN.md risk R-27): a cloned repo must never be able to turn on
// its own untrusted-content loading just by being `cd`'d into (R-02, D-23).
type SkillsCfg struct {
	// Roots is resolved through PathSet.Resolve: a "config:"-prefixed root lives outside
	// the workspace and is trusted (user tier); anything that resolves inside the
	// workspace is untrusted (workspace tier), regardless of how it was spelled.
	Roots []string
	// LoadWorkspace gates discovering any workspace-tier root, and reading the workspace
	// AGENTS.md, at all. Default false: no approval UX exists yet for untrusted content
	// this is injected straight into the system prompt (ticket 2D, not built).
	LoadWorkspace bool `toml:"load_workspace"`
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

// EvalCfg controls what a session records about itself (DESIGN.md 5.13). The workspace
// layer cannot set it: a cloned repo must not be able to switch off the record of what
// the agent did inside it.
type EvalCfg struct {
	Traces bool
}

// SessionCfg bounds what the change journal stores (DESIGN.md 5.15). The workspace
// layer cannot set it: a cloned repo must not be able to size the journal.
type SessionCfg struct {
	SnapshotMaxBytes int64 `toml:"snapshot_max_bytes"`
}

// workspaceLayerCfg is every field the workspace config file (.travis/config.toml) is
// allowed to set. It has no Provider field on purpose (DESIGN.md risk R-27): that file
// ships inside whatever repo the user clones and is read with no action beyond `cd` +
// `travis chat`, so letting it set provider.active or a profile's base_url would let a
// hostile repo redirect the single shared credential (ResolveProviderAPIKey) to a
// host of its choosing. Revisit when the broker (Phase 2, PR 2B) binds credentials to
// their destination instead of trusting whichever file set it — at that point this
// restriction becomes redundant, not complementary, and can be removed.
type workspaceLayerCfg struct {
	Profile string
	Brain   BrainCfg
	Tools   ToolsCfg
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
	for _, layerPath := range []string{pathSet.SystemConfigFile, userConfigFile} {
		layerDiags, err := decodeLayerFile(readFile, layerPath, &cfg)
		diags = append(diags, layerDiags...)
		if err != nil {
			return nil, diags, err
		}
	}

	// The workspace file alone decodes into the restricted workspaceLayerCfg (no
	// Provider field), not Config — see its doc comment. Everything after it in
	// precedence (extra paths, env vars) is trusted the same as system/user config.
	workspaceDiags, err := decodeWorkspaceLayerFile(readFile, pathSet.WorkspaceConfigFile, &cfg)
	diags = append(diags, workspaceDiags...)
	if err != nil {
		return nil, diags, err
	}

	for _, layerPath := range extraPaths {
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
		Brain:   BrainCfg{MaxSteps: 40},
		Tools:   ToolsCfg{ApprovalDefault: "ask", MaxExposed: 60},
		Eval:    EvalCfg{Traces: true},
		Session: SessionCfg{SnapshotMaxBytes: 16 << 20},
		MCP:     MCPCfg{},
		Skills:  SkillsCfg{Roots: []string{"config:skills"}},
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

// decodeWorkspaceLayerFile merges one workspace config file's allowed fields (see
// workspaceLayerCfg) into cfg. A [provider] table in the file is not an error — it's
// silently inert, since workspaceLayerCfg has no field to decode it into — but is
// still reported as a warning so the file's author isn't left wondering why it had no
// effect on the active model.
func decodeWorkspaceLayerFile(readFile fileReader, path string, cfg *Config) (Diagnostics, error) {
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

	if setsProviderTable(data) {
		diags = append(diags, Diagnostic{
			Severity: SeverityWarning,
			Message:  fmt.Sprintf("config %s: [provider] may not be set by the workspace layer (DESIGN.md risk R-27); ignored", path),
		})
	}
	if setsMCPTable(data) {
		diags = append(diags, Diagnostic{
			Severity: SeverityWarning,
			Message:  fmt.Sprintf("config %s: [mcp] may not be set by the workspace layer (DESIGN.md risk R-27; here for code execution, not just credential redirection); ignored", path),
		})
	}
	if setsSkillsTable(data) {
		diags = append(diags, Diagnostic{
			Severity: SeverityWarning,
			Message:  fmt.Sprintf("config %s: [skills] may not be set by the workspace layer (DESIGN.md risk R-27; a workspace must not be able to turn on its own untrusted-content loading, R-02/D-23); ignored", path),
		})
	}

	seed := workspaceLayerCfg{Profile: cfg.Profile, Brain: cfg.Brain, Tools: cfg.Tools}
	if err := toml.Unmarshal(data, &seed); err != nil {
		return diags, fmt.Errorf("config %s: %w", path, err)
	}
	cfg.Profile, cfg.Brain, cfg.Tools = seed.Profile, seed.Brain, seed.Tools
	return diags, nil
}

// setsProviderTable reports whether data has a top-level [provider] table, purely to
// give decodeWorkspaceLayerFile's warning a clear cause. It never affects what gets
// applied — workspaceLayerCfg's shape is what actually enforces the restriction.
func setsProviderTable(data []byte) bool {
	var raw map[string]any
	if err := toml.Unmarshal(data, &raw); err != nil {
		return false
	}
	_, ok := raw["provider"]
	return ok
}

// setsMCPTable reports whether data has a top-level [mcp] table, purely to give
// decodeWorkspaceLayerFile's warning a clear cause; workspaceLayerCfg's shape is what
// actually enforces the restriction.
func setsMCPTable(data []byte) bool {
	var raw map[string]any
	if err := toml.Unmarshal(data, &raw); err != nil {
		return false
	}
	_, ok := raw["mcp"]
	return ok
}

// setsSkillsTable reports whether data has a top-level [skills] table, purely to give
// decodeWorkspaceLayerFile's warning a clear cause; workspaceLayerCfg's shape is what
// actually enforces the restriction.
func setsSkillsTable(data []byte) bool {
	var raw map[string]any
	if err := toml.Unmarshal(data, &raw); err != nil {
		return false
	}
	_, ok := raw["skills"]
	return ok
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

	if c.Session.SnapshotMaxBytes <= 0 {
		diags = append(diags, Diagnostic{Severity: SeverityError, Message: "session.snapshot_max_bytes must be positive"})
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

	// A server with no command is a silent no-op at best and a confusing exec target at
	// worst; this is exec configuration, so it is held to the same hard-fail standard as
	// a malformed [security.*] key, not treated as a soft warning.
	for name, server := range c.MCP.Servers {
		if server.Command == "" {
			diags = append(diags, Diagnostic{Severity: SeverityError, Message: fmt.Sprintf("mcp.servers.%s.command is empty", name)})
		}
	}

	return diags
}
