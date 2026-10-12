// Command travis is the composition root: wiring and flags only.
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"runtime/debug"

	"github.com/Kzeezee/travis/internal/agent/loop"
	"github.com/Kzeezee/travis/internal/config"
	"github.com/Kzeezee/travis/internal/core"
	"github.com/Kzeezee/travis/internal/eval/trace"
	"github.com/Kzeezee/travis/internal/frontends/terminal"
	"github.com/Kzeezee/travis/internal/id"
	"github.com/Kzeezee/travis/internal/log"
	"github.com/Kzeezee/travis/internal/providers/openaicompat"
	"github.com/Kzeezee/travis/internal/security/broker"
	"github.com/Kzeezee/travis/internal/security/redact"
	"github.com/Kzeezee/travis/internal/tools/builtin"
	"github.com/Kzeezee/travis/internal/tools/registry"
)

const shortRevLen = 12

const (
	subscriberEventCapacity = 1024
	// defaultMaxReadBytes is fs_read's own cap. It isn't a key in DESIGN.md 5.5's
	// [tools] example, so it stays a compiled constant rather than a Config field.
	defaultMaxReadBytes = 256 << 10
	// defaultMaxWriteBytes matches the read cap so a future wire write_file fits in one frame.
	defaultMaxWriteBytes = 256 << 10
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
	if err != nil {
		for _, diag := range diags {
			fmt.Fprintf(os.Stderr, "travis: config: %s\n", diag.Message)
		}
		return err
	}
	// Load succeeded, so every remaining diagnostic is a warning the chat view can show.
	startupWarnings := make([]string, 0, len(diags))
	for _, diag := range diags {
		startupWarnings = append(startupWarnings, "config: "+diag.Message)
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
	apiKey := config.ResolveProviderAPIKey()
	provider := openaicompat.BuildProvider(profile.BaseURL, apiKey, nil, core.SystemClock{})
	session, err := assembleChatSession(cfg, paths, provider, profile.Model, os.Stderr, redact.New(apiKey))
	if err != nil {
		return err
	}

	events, unsubscribe := session.bus.Subscribe(nil)
	defer unsubscribe()

	// session.approver is unused until 2B drives real Ask decisions through it; wiring it
	// here means the modal is already end to end once that call site exists.
	approvals := make(chan core.ApprovalRequest)
	session.approver = terminal.BuildApprover(approvals)
	chatErr := terminal.RunChat(context.Background(), events, approvals, session.conversation.RunAssistantTurn,
		terminal.Options{ModelName: profile.Model, StartupWarnings: startupWarnings, Input: in, Output: out})
	session.close()
	return chatErr
}

// chatSession is everything a chat runs on apart from its frontend: the conversation,
// the bus it publishes to, and the trace recording that bus.
type chatSession struct {
	conversation *loop.AssistantConversation
	bus          *core.Bus
	waitForTrace func()
	// approver is set by runChat once it creates the approval channel; 2B's broker-side
	// policy loop will call it wherever a decision evaluates to Ask.
	approver core.Approver
}

// assembleChatSession wires the registry, built-in tools, bus, trace and conversation for
// one session. The provider is a parameter so the end-to-end test can script it while
// everything else stays real; diagnostics receives bus warnings and trace status lines.
func assembleChatSession(cfg *config.Config, paths config.PathSet, provider core.Provider, model string, diagnostics io.Writer, redactor redact.Redactor) (*chatSession, error) {
	toolRegistry := registry.BuildRegistry(cfg.Tools.MaxExposed)
	// In-process and workspace-confined, with no policy until the broker process replaces it (D-18).
	fileBroker := broker.BuildLocalBroker(paths.WorkspaceDir)
	for _, tool := range []core.Tool{
		builtin.BuildFileReadTool(fileBroker, paths.WorkspaceDir, defaultMaxReadBytes),
		builtin.BuildGrepTool(fileBroker, paths.WorkspaceDir),
		builtin.BuildFileWriteTool(fileBroker, paths.WorkspaceDir, defaultMaxWriteBytes, config.ReportNativeLineEnding()),
		builtin.BuildFileEditTool(fileBroker, paths.WorkspaceDir, defaultMaxWriteBytes),
	} {
		if err := toolRegistry.Register(registry.SourceBuiltin, tool); err != nil {
			return nil, err
		}
	}

	bus := core.BuildEventBus(log.BuildTextLogger(diagnostics, log.LevelWarn), subscriberEventCapacity)
	clock := core.SystemClock{}
	ids := id.BuildULIDGenerator(clock)
	deps := loop.Dependencies{Provider: provider, Tools: toolRegistry, Events: redact.WrapSink(bus, redactor), Clock: clock, IDs: ids}

	session := &chatSession{
		conversation: loop.BuildAssistantConversation(deps, model, cfg.Brain.MaxSteps),
		bus:          bus,
		waitForTrace: func() {},
	}
	if cfg.Eval.Traces {
		session.waitForTrace = startSessionTrace(bus, filepath.Join(paths.StateDir, "traces"), ids.GenerateID(), diagnostics)
	}
	return session, nil
}

// close ends the session's event stream, which lets observers finish, then waits for the
// trace to flush.
func (s *chatSession) close() {
	s.bus.Close()
	s.waitForTrace()
}

// startSessionTrace records the bus to a per-session trace. Tracing only observes, so a
// trace that cannot be opened or written is reported and the chat carries on without it.
// The returned function waits, once the bus has closed, for the trace to be flushed.
func startSessionTrace(bus *core.Bus, traceDir string, sessionID core.ID, diagnostics io.Writer) func() {
	file, err := trace.CreateSessionFile(traceDir, sessionID)
	if err != nil {
		fmt.Fprintf(diagnostics, "travis: %v; continuing without a trace\n", err)
		return func() {}
	}

	events, _ := bus.Subscribe(nil)
	recorded := make(chan error, 1)
	go func() { recorded <- trace.RecordEvents(events, file) }()

	return func() {
		err := <-recorded
		if closeErr := file.Close(); err == nil {
			err = closeErr
		}
		if err != nil {
			fmt.Fprintf(diagnostics, "travis: trace %s is incomplete: %v\n", file.Name(), err)
			return
		}
		fmt.Fprintf(diagnostics, "travis: trace written to %s\n", file.Name())
		if dropped := bus.ReportDroppedEventCount(); dropped > 0 {
			fmt.Fprintf(diagnostics, "travis: trace may be missing up to %d events; the bus dropped them\n", dropped)
		}
	}
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
