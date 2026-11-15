package config

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/pelletier/go-toml/v2"

	"github.com/Kzeezee/travis/internal/core"
)

// configDocument is the user config as plain TOML, so a write keeps every value the user set,
// even keys this version of travis does not know. Comments and key order are not kept.
type configDocument map[string]any

// SaveActiveProvider makes profileName the provider every later session starts on.
func SaveActiveProvider(paths PathSet, profileName string) error {
	return updateUserConfig(paths, func(doc configDocument) error {
		doc.ensureTable("provider")["active"] = profileName
		return nil
	})
}

// SaveVoiceOutput turns spoken replies on or off.
func SaveVoiceOutput(paths PathSet, isEnabled bool) error {
	return updateUserConfig(paths, func(doc configDocument) error {
		doc.ensureTable("voice").ensureTable("output")["enabled"] = isEnabled
		return nil
	})
}

// SaveVoiceInput sets push-to-talk and keeps its key ready even while dictation is off.
func SaveVoiceInput(paths PathSet, cfg VoiceInputCfg) error {
	if cfg.Enabled && strings.TrimSpace(cfg.PushToTalkKey) == "" {
		return fmt.Errorf("voice input needs a push-to-talk key: %w", core.ErrInvalid)
	}
	return updateUserConfig(paths, func(doc configDocument) error {
		input := doc.ensureTable("voice").ensureTable("input")
		input["enabled"] = cfg.Enabled
		input["ptt_key"] = cfg.PushToTalkKey
		return nil
	})
}

// SaveUI persists appearance defaults owned only by the user layer.
func SaveUI(paths PathSet, cfg UICfg) error {
	switch cfg.Theme {
	case "auto", "dark", "light", "high_contrast", "mono":
	default:
		return fmt.Errorf("unknown UI theme %q: %w", cfg.Theme, core.ErrInvalid)
	}
	return updateUserConfig(paths, func(doc configDocument) error {
		ui := doc.ensureTable("ui")
		ui["theme"], ui["reduced_motion"] = cfg.Theme, cfg.ReducedMotion
		return nil
	})
}

// SaveCostBudgets persists non-negative per-turn and daily dollar ceilings.
func SaveCostBudgets(paths PathSet, maxTurnUSD, maxDailyUSD float64) error {
	if maxTurnUSD < 0 || maxDailyUSD < 0 {
		return fmt.Errorf("cost budgets cannot be negative: %w", core.ErrInvalid)
	}
	return updateUserConfig(paths, func(doc configDocument) error {
		brain := doc.ensureTable("brain")
		brain["max_turn_cost_usd"], brain["max_daily_cost_usd"] = maxTurnUSD, maxDailyUSD
		return nil
	})
}

// SaveSessionRetention persists how long completed session logs are kept.
func SaveSessionRetention(paths PathSet, retention string) error {
	if _, err := ParseSessionRetention(retention); err != nil {
		return err
	}
	return updateUserConfig(paths, func(doc configDocument) error {
		doc.ensureTable("session")["retention"] = retention
		return nil
	})
}

// SaveProviderModel changes a configured profile's model and drops stale model-specific metadata.
func SaveProviderModel(paths PathSet, profileName string, profile ProviderProfileCfg) error {
	if strings.TrimSpace(profileName) == "" || strings.TrimSpace(profile.Model) == "" || strings.TrimSpace(profile.BaseURL) == "" {
		return fmt.Errorf("provider and model are required: %w", core.ErrInvalid)
	}
	return updateUserConfig(paths, func(doc configDocument) error {
		entry := doc.ensureTable("provider").ensureTable("profiles").ensureTable(profileName)
		entry["kind"], entry["base_url"], entry["model"] = profile.Kind, profile.BaseURL, profile.Model
		if profile.Auth.Kind != "" || profile.Auth.Ref != "" {
			entry["auth"] = map[string]any{"kind": profile.Auth.Kind, "ref": profile.Auth.Ref}
		}
		entry["context_length"], entry["max_tokens"] = 0, 0
		entry["input_usd_per_mtok"], entry["output_usd_per_mtok"] = 0, 0
		return nil
	})
}

// SaveApprovalDefault sets [tools] approval_default, which governs MCP tool calls.
func SaveApprovalDefault(paths PathSet, mode string) error {
	return updateUserConfig(paths, func(doc configDocument) error {
		doc.ensureTable("tools")["approval_default"] = mode
		return nil
	})
}

// SaveSecurityProfile makes profileName the security profile every later session runs under.
func SaveSecurityProfile(paths PathSet, profileName string) error {
	return updateUserConfig(paths, func(doc configDocument) error {
		doc.ensureTable("security")["profile"] = profileName
		return nil
	})
}

// SaveProviderProfile creates the named profile, or updates the endpoint, model and a known context
// length of one that exists, keeping every other setting it has, such as auth or prices.
func SaveProviderProfile(paths PathSet, name string, profile ProviderProfileCfg) error {
	if name == "" || profile.BaseURL == "" || profile.Model == "" {
		return fmt.Errorf("a provider profile needs a name, a base URL and a model: %w", core.ErrInvalid)
	}
	return updateUserConfig(paths, func(doc configDocument) error {
		entry := doc.ensureTable("provider").ensureTable("profiles").ensureTable(name)
		entry["kind"], entry["base_url"], entry["model"] = profile.Kind, profile.BaseURL, profile.Model
		if profile.ContextLength > 0 {
			entry["context_length"] = profile.ContextLength
		}
		return nil
	})
}

// SaveMCPServer adds the named server, or replaces it when one by that name exists.
func SaveMCPServer(paths PathSet, name string, server MCPServerCfg) error {
	if name == "" || server.Command == "" {
		return fmt.Errorf("an MCP server needs a name and a command: %w", core.ErrInvalid)
	}
	return updateUserConfig(paths, func(doc configDocument) error {
		entry := map[string]any{"command": server.Command}
		if len(server.Args) > 0 {
			entry["args"] = server.Args
		}
		if len(server.Env) > 0 {
			entry["env"] = server.Env
		}
		if server.AllowNet {
			entry["allow_net"] = true
		}
		if len(server.ReadPaths) > 0 {
			entry["read_paths"] = server.ReadPaths
		}
		if len(server.WritePaths) > 0 {
			entry["write_paths"] = server.WritePaths
		}
		if tools := map[string]any{}; len(server.Tools.Allow)+len(server.Tools.Deny) > 0 {
			if len(server.Tools.Allow) > 0 {
				tools["allow"] = server.Tools.Allow
			}
			if len(server.Tools.Deny) > 0 {
				tools["deny"] = server.Tools.Deny
			}
			entry["tools"] = tools
		}
		doc.ensureTable("mcp").ensureTable("servers")[name] = entry
		return nil
	})
}

// RemoveMCPServer deletes the named server; removing one that is not configured is not an error.
func RemoveMCPServer(paths PathSet, name string) (wasConfigured bool, err error) {
	err = updateUserConfig(paths, func(doc configDocument) error {
		servers := doc.ensureTable("mcp").ensureTable("servers")
		_, wasConfigured = servers[name]
		delete(servers, name)
		return nil
	})
	return wasConfigured, err
}

// ensureTable returns doc's sub-table key, creating it when missing. A non-table value
// under that key is replaced: the result is checked against Config before anything is written.
func (doc configDocument) ensureTable(key string) configDocument {
	if table, ok := doc[key].(map[string]any); ok {
		return table
	}
	table := map[string]any{}
	doc[key] = table
	return table
}

// updateUserConfig re-reads the user config, applies change, and writes it back atomically, so a
// hand edit made since the last write is never lost. Before the first rewrite of a file that has
// comments it keeps a copy beside it, since a rewrite cannot keep them.
func updateUserConfig(paths PathSet, change func(configDocument) error) error {
	path := paths.ReportUserConfigFile()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create config directory: %w", err)
	}
	unlock, err := lockConfigFile(path + ".lock")
	if err != nil {
		return err
	}
	defer unlock()

	original, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("read %s: %w", path, err)
	}
	doc := configDocument{}
	if err := toml.Unmarshal(original, &doc); err != nil {
		return fmt.Errorf("config %s is not valid TOML, so travis will not rewrite it: %w", path, err)
	}
	if err := change(doc); err != nil {
		return err
	}
	updated, err := toml.Marshal(doc)
	if err != nil {
		return fmt.Errorf("encode %s: %w", path, err)
	}
	candidate := buildDefaultConfig()
	if err := toml.Unmarshal(updated, &candidate); err != nil {
		return fmt.Errorf("config %s would no longer load, so it was left unchanged: %w", path, err)
	}
	if diags := candidate.Validate(); diags.HasErrors() {
		return fmt.Errorf("config %s would be invalid, so it was left unchanged: %s: %w", path, diags[0].Message, core.ErrInvalid)
	}

	if bytes.Contains(original, []byte("#")) {
		if err := backUpOnce(path, original); err != nil {
			return err
		}
	}
	return writeFileAtomically(path, updated)
}

func lockConfigFile(path string) (func(), error) {
	file, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, fmt.Errorf("lock config: %w", err)
	}
	if err := lockExclusive(file); err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("lock config: %w", err)
	}
	return func() {
		_ = unlockExclusive(file)
		_ = file.Close()
	}, nil
}

// backUpOnce keeps the first commented version only; later backups would just copy travis's own output.
func backUpOnce(path string, original []byte) error {
	backup, err := os.OpenFile(path+".bak", os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if errors.Is(err, os.ErrExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("back up %s before rewriting it: %w", path, err)
	}
	_, writeErr := backup.Write(original)
	if closeErr := backup.Close(); writeErr == nil {
		writeErr = closeErr
	}
	if writeErr != nil {
		return fmt.Errorf("back up %s before rewriting it: %w", path, writeErr)
	}
	return nil
}

// writeFileAtomically never leaves a half-written config behind if travis dies mid-write.
func writeFileAtomically(path string, data []byte) error {
	temp, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".*.tmp")
	if err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	defer os.Remove(temp.Name()) // a no-op once the rename succeeds
	_, writeErr := temp.Write(data)
	if closeErr := temp.Close(); writeErr == nil {
		writeErr = closeErr
	}
	if writeErr == nil {
		writeErr = os.Chmod(temp.Name(), 0o600)
	}
	if writeErr == nil {
		writeErr = os.Rename(temp.Name(), path)
	}
	if writeErr != nil {
		return fmt.Errorf("write %s: %w", path, writeErr)
	}
	return nil
}
