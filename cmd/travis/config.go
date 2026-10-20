package main

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/pelletier/go-toml/v2"

	"github.com/Kzeezee/travis/internal/config"
)

const (
	configUsage    = "usage: travis config path|check|show|init"
	redactedMarker = "<redacted>"
)

// starterConfig is written commented-out, so a fresh file changes nothing until a line is enabled.
const starterConfig = `# Travis user config. Uncomment and edit; run "travis config check" to validate.
# Run "travis config show" to see the merged result.

# [provider]
# active = "local"
#
# [provider.profiles.local]
# kind     = "openai_compatible"
# base_url = "http://127.0.0.1:11434/v1"
# model    = "qwen3:14b"

# [brain]
# max_steps = 40

# [eval]
# traces = true

# [voice.output]
# enabled = true
`

func runConfig(args []string, out io.Writer) error {
	if len(args) != 1 {
		return errors.New(configUsage)
	}
	switch args[0] {
	case "path":
		return printConfigPaths(out)
	case "check":
		return checkConfig(out)
	case "show":
		return showConfig(out, os.Stderr)
	case "init":
		return initConfig(out)
	default:
		return fmt.Errorf("unknown config subcommand %q (%s)", args[0], configUsage)
	}
}

func printConfigPaths(out io.Writer) error {
	paths, err := config.BuildCurrentPathSet()
	if err != nil {
		return err
	}
	for _, file := range []struct{ layer, path string }{
		{"system", paths.SystemConfigFile},
		{"user", paths.ReportUserConfigFile()},
		{"workspace", paths.WorkspaceConfigFile},
	} {
		state := "missing"
		if _, err := os.Stat(file.path); err == nil {
			state = "exists"
		}
		fmt.Fprintf(out, "%-9s %s (%s)\n", file.layer, file.path, state)
	}
	return nil
}

// checkConfig prints every diagnostic; the returned error makes the command exit non-zero.
func checkConfig(out io.Writer) error {
	_, diags, err := config.Load()
	printDiagnostics(out, diags)
	if err != nil {
		return err
	}
	fmt.Fprintln(out, "config ok")
	return nil
}

func printDiagnostics(w io.Writer, diags config.Diagnostics) {
	for _, diag := range diags {
		label := "warning"
		if diag.Severity == config.SeverityError {
			label = "error"
		}
		fmt.Fprintf(w, "%s: %s\n", label, diag.Message)
	}
}

// showConfig prints the merged config as TOML on out; warnings go to diagnostics so out stays valid TOML.
func showConfig(out, diagnostics io.Writer) error {
	cfg, diags, err := config.Load()
	printDiagnostics(diagnostics, diags)
	if err != nil {
		return err
	}
	// Config holds no credential value (auth.ref is a reference); MCP env is where users put tokens.
	shown := *cfg
	shown.MCP.Servers = make(map[string]config.MCPServerCfg, len(cfg.MCP.Servers))
	for name, server := range cfg.MCP.Servers {
		redacted := make(map[string]string, len(server.Env))
		for key := range server.Env {
			redacted[key] = redactedMarker
		}
		server.Env = redacted
		shown.MCP.Servers[name] = server
	}
	return toml.NewEncoder(out).Encode(shown)
}

// initConfig writes only to the user config path, and never over an existing file.
func initConfig(out io.Writer) error {
	paths, err := config.BuildCurrentPathSet()
	if err != nil {
		return err
	}
	userFile := paths.ReportUserConfigFile()
	if err := os.MkdirAll(filepath.Dir(userFile), 0o700); err != nil {
		return fmt.Errorf("create config directory: %w", err)
	}
	file, err := os.OpenFile(userFile, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if errors.Is(err, fs.ErrExist) {
		return fmt.Errorf("%s already exists; not overwriting", userFile)
	}
	if err != nil {
		return fmt.Errorf("create %s: %w", userFile, err)
	}
	_, writeErr := file.WriteString(starterConfig)
	if closeErr := file.Close(); writeErr == nil {
		writeErr = closeErr
	}
	if writeErr != nil {
		return fmt.Errorf("write %s: %w", userFile, writeErr)
	}
	fmt.Fprintf(out, "wrote %s\n", userFile)
	return nil
}
