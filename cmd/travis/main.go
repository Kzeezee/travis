// Command travis is the composition root: wiring and flags only.
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"runtime"
	"runtime/debug"

	"github.com/Kzeezee/travis/internal/agent/loop"
	"github.com/Kzeezee/travis/internal/config"
	"github.com/Kzeezee/travis/internal/core"
	"github.com/Kzeezee/travis/internal/frontends/terminal"
	"github.com/Kzeezee/travis/internal/id"
	"github.com/Kzeezee/travis/internal/log"
	"github.com/Kzeezee/travis/internal/providers/openaicompat"
	"github.com/Kzeezee/travis/internal/tools/builtin"
	"github.com/Kzeezee/travis/internal/tools/registry"
)

const shortRevLen = 12

const (
	subscriberEventCapacity = 1024
	// defaultMaxReadBytes is fs.read's own cap. It isn't a key in DESIGN.md 5.5's
	// [tools] example, so it stays a compiled constant rather than a Config field.
	defaultMaxReadBytes = 256 << 10
)

// Overridden at release time via: go build -ldflags "-X main.version=v0.1.0"
var version = "dev"

func main() {
	if err := run(os.Args[1:], os.Stdin, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "travis:", err)
		os.Exit(1)
	}
}

func run(args []string, in io.Reader, out io.Writer) error {
	if len(args) == 0 {
		return fmt.Errorf("no command given (try: travis chat)")
	}

	switch args[0] {
	case "version", "--version", "-v":
		fmt.Fprintln(out, buildVersionString())
		return nil
	case "chat":
		return runChat(args[1:], in, out)
	default:
		return fmt.Errorf("unknown command %q (try: travis chat, travis version)", args[0])
	}
}

// runChat assembles the text-assistant slice: a provider adapter, the assistant
// conversation, the event bus they publish to, and the frontend that subscribes to it.
// This is the only place that knows which concrete pieces are in play.
func runChat(args []string, in io.Reader, out io.Writer) error {
	flags := flag.NewFlagSet("chat", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	baseURL := flags.String("base-url", "", "override the active provider profile's endpoint base URL")
	model := flags.String("model", "", "override the active provider profile's model name")
	if err := flags.Parse(args); err != nil {
		return err
	}
	explicitFlags := map[string]bool{}
	flags.Visit(func(f *flag.Flag) { explicitFlags[f.Name] = true })

	cfg, diags, err := config.Load()
	for _, diag := range diags {
		fmt.Fprintf(os.Stderr, "travis: config: %s\n", diag.Message)
	}
	if err != nil {
		return err
	}

	// CLI flags are the highest layer in DESIGN.md 5.2's precedence, above config
	// files and env vars, but only when the user actually passed one — an unset
	// flag must not clobber whatever config.Load resolved.
	profile := cfg.Provider.Profiles[cfg.Provider.Active]
	if explicitFlags["base-url"] {
		profile.BaseURL = *baseURL
	}
	if explicitFlags["model"] {
		profile.Model = *model
	}

	paths, err := config.BuildCurrentPathSet()
	if err != nil {
		return err
	}
	toolRegistry := registry.BuildRegistry(cfg.Tools.MaxExposed)
	for _, tool := range []core.Tool{
		builtin.BuildFileReadTool(paths.WorkspaceDir, defaultMaxReadBytes),
		builtin.BuildGrepTool(paths.WorkspaceDir),
	} {
		if err := toolRegistry.Register(registry.SourceBuiltin, tool); err != nil {
			return err
		}
	}

	bus := core.BuildEventBus(log.BuildTextLogger(os.Stderr, log.LevelWarn), subscriberEventCapacity)
	defer bus.Close()

	provider := openaicompat.BuildProvider(profile.BaseURL, config.ResolveProviderAPIKey(), nil)
	clock := core.SystemClock{}
	deps := loop.Dependencies{Provider: provider, Tools: toolRegistry, Events: bus, Clock: clock, IDs: id.BuildULIDGenerator(clock)}
	assistantConversation := loop.BuildAssistantConversation(deps, profile.Model, cfg.Brain.MaxSteps)

	events, unsubscribe := bus.Subscribe(nil)
	defer unsubscribe()

	return terminal.RunChat(context.Background(), events, assistantConversation.RunAssistantTurn,
		terminal.Options{ModelName: profile.Model, Input: in, Output: out})
}

func buildVersionString() string {
	revision, dirty := readVCSStamp()

	build := "travis " + version
	switch {
	case revision != "" && dirty:
		build += " (" + revision + ", dirty)"
	case revision != "":
		build += " (" + revision + ")"
	}

	return build + "\n" + runtime.Version() + " " + config.ReportRuntimePlatform()
}

// Stamps are absent for `go run` and for builds from an archive rather than a checkout.
func readVCSStamp() (revision string, dirty bool) {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return "", false
	}

	for _, setting := range info.Settings {
		switch setting.Key {
		case "vcs.revision":
			revision = setting.Value
			if len(revision) > shortRevLen {
				revision = revision[:shortRevLen]
			}
		case "vcs.modified":
			dirty = setting.Value == "true"
		}
	}

	return revision, dirty
}
