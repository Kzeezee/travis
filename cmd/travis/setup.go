package main

import (
	"context"
	"errors"
	"io"
	"maps"
	"os"
	"slices"
	"strings"

	"github.com/Kzeezee/travis/internal/config"
	"github.com/Kzeezee/travis/internal/frontends/terminal"
)

// runInit runs the setup wizard; `travis init` and a first `travis chat` both start here.
func runInit(args []string, in io.Reader, out io.Writer) error {
	if len(args) != 0 {
		return errors.New("usage: travis init")
	}
	_, err := runSetupWizard(in, out)
	return err
}

// runSetupWizard reports whether the user finished; false with no error means they quit part-way.
func runSetupWizard(in io.Reader, out io.Writer) (bool, error) {
	cfg, _, err := config.Load()
	if err != nil {
		return false, err
	}
	paths, err := config.BuildCurrentPathSet()
	if err != nil {
		return false, err
	}
	providers := &providerSwitcher{profiles: cfg.Provider.Profiles, resolveKey: config.ResolveProviderAPIKey, active: cfg.Provider.Active}
	actions := terminal.SetupActions{
		DescribeProviders: providers.DescribeProviders,
		SaveProvider: func(profile, apiKey string) error {
			if strings.TrimSpace(apiKey) != "" {
				if err := config.SaveProviderAPIKey(profile, cfg.Provider.Profiles[profile].Auth, apiKey); err != nil {
					return err
				}
			}
			return config.SaveActiveProvider(paths, profile)
		},
		SaveVoiceOutput:  func(isEnabled bool) error { return config.SaveVoiceOutput(paths, isEnabled) },
		ListMCPServers:   func() []string { return slices.Sorted(maps.Keys(cfg.MCP.Servers)) },
		DescribeSecurity: func() []string { return describeSecurity(cfg, paths) },
	}
	isFinished, err := terminal.RunSetup(context.Background(), actions, terminal.SetupOptions{
		Input: in, Output: out, ConfigFile: paths.ReportUserConfigFile(), IsVoiceOutputEnabled: cfg.Voice.Output.Enabled,
	})
	if err != nil || !isFinished {
		return isFinished, err
	}
	// Finishing with every step skipped saves nothing, and a missing file would offer setup on every chat.
	if _, statErr := os.Stat(paths.ReportUserConfigFile()); errors.Is(statErr, os.ErrNotExist) {
		return true, config.SaveActiveProvider(paths, cfg.Provider.Active)
	}
	return true, nil
}

// shouldOfferSetup is true on a first run: no user config yet and a person at the keyboard.
func shouldOfferSetup(in io.Reader) bool {
	paths, err := config.BuildCurrentPathSet()
	if err != nil {
		return false
	}
	_, err = os.Stat(paths.ReportUserConfigFile())
	return errors.Is(err, os.ErrNotExist) && isInteractiveInput(in)
}

// describeSecurity states the default profile chat runs under (D-27), in the user's terms.
func describeSecurity(cfg *config.Config, paths config.PathSet) []string {
	commands := "nothing until you allow it ([security] exec_allow)"
	if len(cfg.Security.ExecAllow) > 0 {
		commands = strings.Join(cfg.Security.ExecAllow, ", ") + ", asking first unless you said always"
	}
	hosts := "nothing until you allow it ([security] host_allow)"
	if len(cfg.Security.HostAllow) > 0 {
		hosts = strings.Join(cfg.Security.HostAllow, ", ")
	}
	sandbox := "required: a server that cannot be sandboxed does not start"
	if !cfg.Security.RequireSandbox {
		sandbox = "best effort: a server runs unconfined where the OS has no sandbox"
	}
	return []string{
		"can read and write   " + paths.WorkspaceDir + " (the folder you start travis in)",
		"can run              " + commands,
		"can reach            " + hosts,
		"never touches        SSH keys, cloud credentials, .env files and travis's own state,",
		"                     even inside the workspace — enforced by the broker, not by the model",
		"MCP servers          " + sandbox,
		"undo                 /undo rewinds files travis changed this session",
	}
}
