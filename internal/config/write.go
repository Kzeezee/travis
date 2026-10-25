package config

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"

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
	if err := toml.Unmarshal(updated, &Config{}); err != nil {
		return fmt.Errorf("config %s would no longer load, so it was left unchanged: %w", path, err)
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create config directory: %w", err)
	}
	if bytes.Contains(original, []byte("#")) {
		if err := backUpOnce(path, original); err != nil {
			return err
		}
	}
	return writeFileAtomically(path, updated)
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
