package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
	"slices"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/Kzeezee/travis/internal/config"
	"github.com/Kzeezee/travis/internal/core"
	"github.com/Kzeezee/travis/internal/providers/openaicompat"
	"github.com/Kzeezee/travis/internal/security/redact"
)

const (
	providerUsage = "usage: travis provider list"
	mcpUsage      = "usage: travis mcp list"
	// listModelsTimeout bounds `travis models`, which waits on a remote endpoint.
	listModelsTimeout = 30 * time.Second
)

// runProvider lists provider profiles the way /provider does in chat, without reaching any endpoint.
func runProvider(args []string, out io.Writer) error {
	if len(args) != 1 || args[0] != "list" {
		return errors.New(providerUsage)
	}
	cfg, _, err := config.Load()
	if err != nil {
		return err
	}
	switcher := &providerSwitcher{profiles: cfg.Provider.Profiles, resolveKey: config.ResolveProviderAPIKey, active: cfg.Provider.Active}
	table := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	for _, provider := range switcher.DescribeProviders() {
		marker := " "
		if provider.IsActive {
			marker = "*"
		}
		key := "no key yet"
		switch {
		case !provider.IsKeyRequired:
			key = "local, no key needed"
		case provider.HasKey:
			key = "key available"
		}
		fmt.Fprintf(table, "%s %s\t%s\t%s\t%s\n", marker, provider.Name, provider.Model, cfg.Provider.Profiles[provider.Name].BaseURL, key)
	}
	return table.Flush()
}

// runModels lists the models the active provider profile's endpoint offers.
func runModels(args []string, out io.Writer) error {
	if len(args) != 0 {
		return errors.New("usage: travis models")
	}
	cfg, _, err := config.Load()
	if err != nil {
		return err
	}
	profile := cfg.Provider.Profiles[cfg.Provider.Active]
	apiKey, err := config.ResolveProviderAPIKey(cfg.Provider.Active, profile.Auth)
	if err != nil {
		return err
	}
	if apiKey == "" && config.ReportIsCredentialRequired(profile) {
		return fmt.Errorf("provider %q has no API key; add one with /provider %s in travis chat", cfg.Provider.Active, cfg.Provider.Active)
	}
	ctx, cancel := context.WithTimeout(context.Background(), listModelsTimeout)
	defer cancel()
	names, err := openaicompat.BuildProvider(profile.BaseURL, apiKey, nil, core.SystemClock{}).ListModels(ctx)
	if err != nil {
		return fmt.Errorf("list models from provider %q: %w", cfg.Provider.Active, err)
	}
	slices.Sort(names)
	for _, name := range names {
		fmt.Fprintln(out, name)
	}
	return nil
}

// runMCP lists configured MCP servers; whether each one started is only known inside a chat (/mcp).
func runMCP(args []string, out io.Writer) error {
	if len(args) != 1 || args[0] != "list" {
		return errors.New(mcpUsage)
	}
	cfg, _, err := config.Load()
	if err != nil {
		return err
	}
	if len(cfg.MCP.Servers) == 0 {
		fmt.Fprintln(out, "no MCP servers configured; add them under [mcp.servers] in your user config")
		return nil
	}
	table := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	for _, name := range slices.Sorted(maps.Keys(cfg.MCP.Servers)) {
		server := cfg.MCP.Servers[name]
		// Args can carry a token, as `config show` assumes of env; scrub known secret formats before printing.
		fmt.Fprintf(table, "%s\t%s\n", name, redact.New().RedactText(strings.Join(append([]string{server.Command}, server.Args...), " ")))
	}
	return table.Flush()
}
