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
	"github.com/Kzeezee/travis/internal/log"
	"github.com/Kzeezee/travis/internal/providers/openai"
)

const shortRevLen = 12

// Compiled defaults for the model provider. Phase 1E replaces these with the bottom
// layer of DESIGN.md 5.2's config precedence; until then they are the only defaults.
const (
	defaultBaseURL          = "https://api.openai.com/v1"
	defaultModel            = "gpt-4o-mini"
	subscriberEventCapacity = 1024
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

// runChat assembles the text-assistant slice: a provider adapter, the turn runner, the
// event bus they publish to, and the frontend that subscribes to it. This is the only
// place that knows which concrete pieces are in play.
func runChat(args []string, in io.Reader, out io.Writer) error {
	flags := flag.NewFlagSet("chat", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	baseURL := flags.String("base-url", defaultBaseURL, "OpenAI-compatible endpoint base URL")
	model := flags.String("model", defaultModel, "model name to send with each request")
	if err := flags.Parse(args); err != nil {
		return err
	}

	bus := core.BuildEventBus(log.BuildTextLogger(os.Stderr, log.LevelWarn), subscriberEventCapacity)
	defer bus.Close()

	provider := openai.BuildProvider(*baseURL, config.ResolveProviderAPIKey(), nil)
	runner := loop.BuildRunner(provider, bus, core.SystemClock{}, *model)

	events, unsubscribe := bus.Subscribe(nil)
	defer unsubscribe()

	return terminal.BuildApp(events, runner.Run, in, out).Run(context.Background())
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
