package main

import (
	"context"
	"errors"
	"io"
	"maps"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"

	"github.com/Kzeezee/travis/internal/config"
	"github.com/Kzeezee/travis/internal/frontends/terminal"
	"github.com/Kzeezee/travis/internal/providers/openaicompat"
	"github.com/Kzeezee/travis/internal/security/sandbox"
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
	setupProviders := &setupProviders{paths: paths, profiles: cfg.Provider.Profiles, probes: localServerProbes}
	actions := terminal.SetupActions{
		DescribeProviders:   providers.DescribeProviders,
		DetectLocalServers:  setupProviders.DetectLocalServers,
		SaveProvider:        setupProviders.SaveProvider,
		SaveVoiceOutput:     func(isEnabled bool) error { return config.SaveVoiceOutput(paths, isEnabled) },
		ListMCPServers:      func() []string { return slices.Sorted(maps.Keys(cfg.MCP.Servers)) },
		DescribeSecurity:    func(profile string) []string { return describeSecurity(cfg, paths, profile) },
		SaveSecurityProfile: func(profile string) error { return config.SaveSecurityProfile(paths, profile) },
		RecordProgress:      func(step string) { recordSetupProgress(paths.StateDir, step) },
	}
	isFinished, err := terminal.RunSetup(context.Background(), actions, terminal.SetupOptions{
		Input: in, Output: out, ConfigFile: paths.ReportUserConfigFile(), IsVoiceOutputEnabled: cfg.Voice.Output.Enabled,
		SecurityProfile: cfg.Security.Profile, StartStep: readSetupProgress(paths.StateDir),
	})
	if err != nil || !isFinished {
		return isFinished, err
	}
	return true, finishSetup(paths, cfg.Provider.Active)
}

// finishSetup forgets where an earlier run stopped and makes sure the user config exists.
func finishSetup(paths config.PathSet, activeProvider string) error {
	_ = os.Remove(filepath.Join(paths.StateDir, setupProgressFile))
	// Finishing with every step skipped saves nothing, and a missing file would offer setup on every chat.
	if _, statErr := os.Stat(paths.ReportUserConfigFile()); errors.Is(statErr, os.ErrNotExist) {
		return config.SaveActiveProvider(paths, activeProvider)
	}
	return nil
}

// setupProgressFile names the wizard step a quit run stopped at, so the next run opens there.
const setupProgressFile = "setup-progress"

// readSetupProgress reports the saved step; a missing file reads as empty, which opens the first step.
func readSetupProgress(stateDir string) string {
	step, _ := os.ReadFile(filepath.Join(stateDir, setupProgressFile))
	return strings.TrimSpace(string(step))
}

// recordSetupProgress is best effort: losing it only means the next run starts at the first step.
func recordSetupProgress(stateDir, step string) {
	if os.MkdirAll(stateDir, 0o700) == nil {
		_ = os.WriteFile(filepath.Join(stateDir, setupProgressFile), []byte(step), 0o600)
	}
}

// localServerProbe is a model server the wizard looks for, saved as profile when chosen.
type localServerProbe struct {
	profile, origin string
	detect          func(ctx context.Context, origin string) openaicompat.LocalServer
}

var localServerProbes = []localServerProbe{
	{profile: "ollama", origin: "http://127.0.0.1:11434", detect: openaicompat.DetectOllama},
	{profile: "llamacpp", origin: "http://127.0.0.1:8080", detect: openaicompat.DetectLlamaCpp},
}

// setupProviders backs the wizard's provider step. A local server it detected stands in for the
// configured profile of the same name, and choosing it creates or updates that profile.
type setupProviders struct {
	paths    config.PathSet
	profiles map[string]config.ProviderProfileCfg
	probes   []localServerProbe

	mu       sync.Mutex
	detected map[string]config.ProviderProfileCfg
}

// DetectLocalServers probes every server at once, so the wait is the slowest probe, not their sum.
func (s *setupProviders) DetectLocalServers() []terminal.LocalServerSummary {
	found := make([]openaicompat.LocalServer, len(s.probes))
	var probing sync.WaitGroup
	for i, probe := range s.probes {
		probing.Go(func() { found[i] = probe.detect(context.Background(), probe.origin) })
	}
	probing.Wait()

	s.mu.Lock()
	defer s.mu.Unlock()
	s.detected = map[string]config.ProviderProfileCfg{}
	summaries := make([]terminal.LocalServerSummary, len(s.probes))
	for i, probe := range s.probes {
		server := found[i]
		address := probe.origin
		if origin, err := url.Parse(probe.origin); err == nil {
			address = origin.Host
		}
		summaries[i] = terminal.LocalServerSummary{Profile: probe.profile, Address: address, Model: server.Model,
			ContextLength: server.ContextLength, IsDetected: server.IsDetected}
		if server.Model != "" {
			s.detected[probe.profile] = config.ProviderProfileCfg{Kind: "openai_compatible", BaseURL: server.BaseURL,
				Model: server.Model, ContextLength: server.ContextLength}
		}
	}
	return summaries
}

// SaveProvider writes a detected local server's profile, or stores apiKey for a configured one, then makes it active.
func (s *setupProviders) SaveProvider(profile, apiKey string) error {
	s.mu.Lock()
	local, isLocal := s.detected[profile]
	s.mu.Unlock()
	switch {
	case isLocal:
		if err := config.SaveProviderProfile(s.paths, profile, local); err != nil {
			return err
		}
	case strings.TrimSpace(apiKey) != "":
		if err := config.SaveProviderAPIKey(profile, s.profiles[profile].Auth, apiKey); err != nil {
			return err
		}
	}
	return config.SaveActiveProvider(s.paths, profile)
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

// describeSecurity states what profile lets chat do (capabilities.BuildProfileSet, D-27), in the user's terms.
func describeSecurity(cfg *config.Config, paths config.PathSet, profile string) []string {
	reads, writes := paths.WorkspaceDir+" (the folder you start travis in)", "the same folder"
	commands := "nothing until you allow it ([security] exec_allow)"
	if len(cfg.Security.ExecAllow) > 0 {
		commands = strings.Join(cfg.Security.ExecAllow, ", ") + ", asking first unless you said always"
	}
	hosts := "nothing until you allow it ([security] host_allow)"
	if len(cfg.Security.HostAllow) > 0 {
		hosts = strings.Join(cfg.Security.HostAllow, ", ")
	}
	servers := "required: a server that cannot be sandboxed does not start"
	if !cfg.Security.RequireSandbox {
		servers = "best effort: a server runs unconfined where the OS has no sandbox"
	}
	switch profile {
	case "paranoid":
		writes, commands, hosts, servers = "nothing", "nothing", "nothing", "none start, since nothing may run"
	case "dev":
		reads, writes = "your home folder", paths.WorkspaceDir+" and the OS temp folder"
		commands, hosts = "any command, asking first unless you said always", "any host"
	}
	osSandbox := "none on this OS"
	if tier, err := sandbox.ReportAvailability(); err == nil {
		osSandbox = string(tier) + " available"
	}
	return []string{
		"can read             " + reads,
		"can write            " + writes,
		"can run              " + commands,
		"can reach            " + hosts,
		"never touches        SSH keys, cloud credentials, .env files and travis's own state,",
		"                     even inside the workspace — enforced by the broker, not by the model",
		"MCP servers          " + servers,
		"OS sandbox           " + osSandbox,
		"undo                 /undo rewinds files travis changed this session",
	}
}
