// Command travis is the composition root: wiring and flags only.
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"time"

	"github.com/Kzeezee/travis/internal/agent/loop"
	"github.com/Kzeezee/travis/internal/config"
	"github.com/Kzeezee/travis/internal/core"
	"github.com/Kzeezee/travis/internal/eval/trace"
	"github.com/Kzeezee/travis/internal/frontends/terminal"
	"github.com/Kzeezee/travis/internal/frontends/voicein"
	"github.com/Kzeezee/travis/internal/frontends/voiceout"
	"github.com/Kzeezee/travis/internal/id"
	"github.com/Kzeezee/travis/internal/log"
	"github.com/Kzeezee/travis/internal/providers/openaicompat"
	"github.com/Kzeezee/travis/internal/security/broker"
	"github.com/Kzeezee/travis/internal/security/policy"
	"github.com/Kzeezee/travis/internal/security/redact"
	"github.com/Kzeezee/travis/internal/state/memory"
	"github.com/Kzeezee/travis/internal/tools/builtin"
	"github.com/Kzeezee/travis/internal/tools/mcp"
	"github.com/Kzeezee/travis/internal/tools/registry"
	"github.com/Kzeezee/travis/internal/tools/skills"
)

const shortRevLen = 12

const (
	subscriberEventCapacity = 1024
	// defaultMaxReadBytes is fs_read's own cap. It isn't a key in DESIGN.md 5.5's
	// [tools] example, so it stays a compiled constant rather than a Config field.
	defaultMaxReadBytes = 256 << 10
	// defaultMaxWriteBytes matches the read cap and fits inside broker.MaxWriteFileBytes.
	defaultMaxWriteBytes = 256 << 10
	// transcriptionTimeout bounds one dictation's trip to the speech-to-text server; a stuck
	// server leaves the input box empty rather than the status bar saying "transcribing" forever.
	transcriptionTimeout = 60 * time.Second
)

// Overridden at release time via: go build -ldflags "-X main.version=v0.1.0"
var version = "dev"

// brokerSHA256 is travis-broker's hex hash, stamped by `make build` with
// -ldflags "-X main.brokerSHA256=..."; chat refuses any other broker binary, or none (R-23).
var brokerSHA256 string

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
	case "run":
		return runHeadless(args[1:], out)
	case "config":
		return runConfig(args[1:], out)
	case "provider":
		return runProvider(args[1:], out)
	case "models":
		return runModels(args[1:], out)
	case "mcp":
		return runMCP(args[1:], out)
	default:
		return fmt.Errorf("unknown command %q (try: travis chat, travis config, travis provider list, travis models, travis mcp list, travis version)", args[0])
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
	// Not cfg.Profile, for the same reason startSessionBroker below pins "default": the
	// workspace config layer can set that, and a cloned repo must not choose its own
	// security profile. This is the same policy construction the broker process uses
	// (BuildSessionPolicy), reused here because MCP server launches are exec-capability
	// decisions made in this process, not round-tripped through the broker. The servers
	// themselves then run inside the OS sandbox (mcp.Sandboxing below).
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	mcpPolicy, err := buildMCPPolicy(cfg, paths, home)
	if err != nil {
		return err
	}
	apiKey, err := config.ResolveProviderAPIKey(cfg.Provider.Active, profile.Auth)
	if err != nil {
		return err
	}
	if apiKey == "" && config.ReportIsCredentialRequired(profile) {
		startupWarnings = append(startupWarnings, fmt.Sprintf("provider %q has no API key; /provider %s asks for one", cfg.Provider.Active, cfg.Provider.Active))
	}
	redactor := redact.New(apiKey)
	bus := buildSessionBus(os.Stderr)
	approvals := make(chan core.ApprovalRequest)
	questions := make(chan core.QuestionRequest)
	approver := terminal.BuildApprover(approvals)
	questioner := terminal.BuildQuestioner(questions)
	narrator, voiceWarning := startVoiceOutput(cfg.Voice.Output, bus, os.Stderr)
	if voiceWarning != "" {
		startupWarnings = append(startupWarnings, voiceWarning)
	}
	if narrator != nil {
		defer narrator.Close()
		approver = voiceout.WrapApprover(approver, narrator)
		questioner = voiceout.WrapQuestioner(questioner, narrator)
	}
	sessionID := id.BuildULIDGenerator(core.SystemClock{}).GenerateID()
	sessionBroker, err := startSessionBroker(paths, sessionID, cfg.Session, os.Stderr, broker.RemoteOptions{
		Events:   redact.WrapSink(bus, redactor),
		Approver: approver,
		Clock:    core.SystemClock{},
	})
	if err != nil {
		return err
	}
	defer func() {
		if closeErr := sessionBroker.Close(); closeErr != nil {
			fmt.Fprintf(os.Stderr, "travis: %v\n", closeErr)
		}
	}()

	provider := openaicompat.BuildProvider(profile.BaseURL, apiKey, nil, core.SystemClock{})
	session, err := assembleChatSession(chatSessionDeps{
		Cfg: cfg, Paths: paths, Session: sessionID, Provider: provider, Model: profile.Model,
		Files: sessionBroker.Remote, Log: sessionBroker.Remote,
		Memory: sessionBroker.Remote, Commands: sessionBroker.Remote, Pages: sessionBroker.Remote,
		Bus: bus, Diagnostics: os.Stderr, Redactor: redactor,
		Questioner: questioner, Approver: approver, MCPPolicy: mcpPolicy, IsSpeechEnabled: narrator != nil,
		MCPSandboxing: mcp.Sandboxing{
			// travis-broker doubles as the sandbox launcher; its hash is checked again before every launch.
			ResolveLauncher: func() (string, error) { return broker.ResolveVerifiedBrokerExecutable(brokerSHA256) },
			IsRequired:      cfg.Security.RequireSandbox,
		},
	})
	if err != nil {
		return err
	}

	events, unsubscribe := session.bus.Subscribe(nil)
	defer unsubscribe()

	startDictation, voiceInputWarning := buildDictation(cfg.Voice.Input)
	if voiceInputWarning != "" {
		startupWarnings = append(startupWarnings, voiceInputWarning)
	}

	switcher := &providerSwitcher{
		profiles: cfg.Provider.Profiles, conversation: session.conversation, redactor: redactor,
		resolveKey: config.ResolveProviderAPIKey, saveKey: config.SaveProviderAPIKey,
		recordChoice: func(profile string) error { return config.RecordProviderChoice(paths, profile) },
		buildProvider: func(baseURL, key string) modelListingProvider {
			return openaicompat.BuildProvider(baseURL, key, nil, core.SystemClock{})
		},
		current: provider,
		active:  cfg.Provider.Active,
	}
	actions := terminal.ChatActions{
		RunTurn:               session.conversation.RunAssistantTurn,
		ResetHistory:          session.conversation.ResetHistory,
		SelectModel:           session.conversation.SelectModel,
		SwitchProvider:        switcher.SwitchProvider,
		SwitchProviderWithKey: switcher.SwitchProviderWithKey,
		ListProviders:         switcher.ListProviders,
		DescribeProviders:     switcher.DescribeProviders,
		ListMCPServers:        session.listMCPServers,
		ListModels:            switcher.ListModels,
		ListTools:             session.reportToolNames,
		ListSkills:            session.reportSkillSummaries,
		ReadMemory:            session.memoryStore.ReadMemory,
		Undo:                  terminal.UndoActions{ListTurns: session.conversation.ListTurns, Rewind: session.conversation.Rewind},

		StartDictation: startDictation,
	}
	chatErr := terminal.RunChat(context.Background(), events, approvals, actions,
		terminal.Options{ModelName: profile.Model, StartupWarnings: startupWarnings, Input: in, Output: out, Questions: questions, PushToTalkKey: cfg.Voice.Input.PushToTalkKey})
	session.close()
	return chatErr
}

// buildMCPPolicy is the policy MCP server launches run under: the default profile plus the
// user's exec_allow, which config refuses to take from the workspace layer.
func buildMCPPolicy(cfg *config.Config, paths config.PathSet, home string) (policy.Policy, error) {
	return broker.BuildSessionPolicy("default", broker.SessionRoots{
		Paths: paths, Home: home, Temp: os.TempDir(),
		ExecAllow: cfg.Security.ExecAllow, HostAllow: cfg.Security.HostAllow,
	})
}

// chatSession is everything a chat runs on apart from its frontend: the conversation,
// the bus it publishes to, and the trace recording that bus.
type chatSession struct {
	conversation  *loop.AssistantConversation
	bus           *core.Bus
	closeMCP      func()
	mcpManager    *mcp.Manager
	toolRegistry  *registry.Registry
	skillsCatalog *skills.Catalog
	memoryStore   core.MemoryStore
	waitForTrace  func()
}

// listMCPServers reports every configured MCP server's live state for /mcp.
func (s *chatSession) listMCPServers() []terminal.MCPServerSummary {
	var servers []terminal.MCPServerSummary
	for _, status := range s.mcpManager.ReportServerStatuses() {
		servers = append(servers, terminal.MCPServerSummary{Name: status.Name, State: string(status.State), Reason: status.Reason, ToolCount: status.ToolCount})
	}
	return servers
}

// reportToolNames lists every tool registered right now, live — not a startup snapshot, so
// an MCP server that connects or dies mid-session is reflected immediately (/tools, 3C).
func (s *chatSession) reportToolNames() []string {
	var names []string
	for _, tool := range s.toolRegistry.List(registry.Filter{}) {
		names = append(names, tool.Name())
	}
	return names
}

// reportSkillSummaries converts the skills catalog's own Tier type to a plain string, the
// only place that conversion happens, so the terminal package never imports internal/tools/skills.
func (s *chatSession) reportSkillSummaries() []terminal.SkillSummary {
	var out []terminal.SkillSummary
	for _, summary := range s.skillsCatalog.ReportSkillSummaries() {
		out = append(out, terminal.SkillSummary{Name: summary.Name, Tier: summary.Tier.String(), Description: summary.Description})
	}
	return out
}

// startVoiceOutput starts speaking what the bus marks for speech when voice output is on. A
// machine with no speech program keeps the chat and turns voice off, saying why (R-19).
func startVoiceOutput(cfg config.VoiceOutputCfg, bus *core.Bus, diagnostics io.Writer) (*voiceout.Narrator, string) {
	if !cfg.Enabled {
		return nil, ""
	}
	speaker, err := voiceout.BuildSystemSpeaker()
	if err != nil {
		return nil, fmt.Sprintf("voice: %v; voice output is off", err)
	}
	narrator := voiceout.StartNarrator(speaker, log.BuildTextLogger(diagnostics, log.LevelWarn))
	speech, _ := bus.Subscribe(func(event core.Event) bool { return event.ReportEventType() == core.EventSpeechRequested })
	go narrator.SpeakRequestedSpeech(speech)
	return narrator, ""
}

// buildDictation is push-to-talk when voice input is on. A missing travis-audio, or an OS with no
// capture backend, keeps the chat and turns voice input off, saying why (R-19).
func buildDictation(cfg config.VoiceInputCfg) (func(context.Context) (func() (string, error), error), string) {
	if !cfg.Enabled {
		return nil, ""
	}
	if !voicein.ReportIsCaptureSupported() {
		return nil, "voice: this OS has no microphone support; voice input is off"
	}
	helper, err := voicein.ResolveHelperExecutable()
	if err != nil {
		return nil, fmt.Sprintf("voice: %v; voice input is off", err)
	}
	client := &http.Client{Timeout: transcriptionTimeout}
	dictation := voicein.BuildDictation(voicein.BuildHelperCapturer(helper), voicein.BuildHTTPTranscriber(cfg.BaseURL, cfg.Model, client))
	return dictation.StartDictation, ""
}

// buildSessionBus is the bus one chat session publishes to; diagnostics receives its warnings.
func buildSessionBus(diagnostics io.Writer) *core.Bus {
	return core.BuildEventBus(log.BuildTextLogger(diagnostics, log.LevelWarn), subscriberEventCapacity)
}

// startSessionBroker launches this session's travis-broker and connects to it. A chat never
// runs without one: there is no unenforced fallback (§5.12.5). The broker keeps the session's
// log under sessionID and backs up files up to sessionCfg's limit before writing them.
func startSessionBroker(paths config.PathSet, sessionID core.ID, sessionCfg config.SessionCfg, diagnostics io.Writer, client broker.RemoteOptions) (*broker.SessionBroker, error) {
	brokerExecutable, err := broker.ResolveBrokerExecutable()
	if err != nil {
		return nil, fmt.Errorf("start the security broker: %w", err)
	}
	sessionBroker, err := broker.StartSessionBroker(context.Background(), broker.SessionLaunch{
		Executable:     brokerExecutable,
		ExpectedSHA256: brokerSHA256,
		WorkspaceDir:   paths.WorkspaceDir,
		RuntimeDir:     paths.RuntimeDir,
		// Not cfg.Profile: the workspace config layer can set that, and a cloned repo must not
		// choose its own security profile. Selectable once [security] config exists.
		Profile:          "default",
		SessionID:        sessionID,
		SnapshotMaxBytes: sessionCfg.SnapshotMaxBytes,
		Diagnostics:      diagnostics,
		Client:           client,
		// The broker performs file effects only; the provider credential stays in travis.
		Env: config.BuildEnvironmentWithoutSecrets(),
	})
	if err != nil {
		return nil, fmt.Errorf("start the security broker: %w", err)
	}
	return sessionBroker, nil
}

// chatSessionDeps is everything one chat session is assembled from. It is a struct rather than
// a parameter list because the end-to-end and scenario tests substitute a different subset each
// time, and reading which of sixteen positional arguments was which had become guesswork.
//
// The optional fields are the ones a test with no human and no broker process leaves out; the
// rest are required, and assembleChatSession says which is missing rather than panicking on it.
type chatSessionDeps struct {
	Cfg      *config.Config
	Paths    config.PathSet
	Session  core.ID
	Provider core.Provider
	Model    string
	// Files performs the file effects the built-in tools ask for.
	Files core.Broker
	// Bus is where this session publishes; Diagnostics receives trace and MCP status lines.
	Bus         *core.Bus
	Diagnostics io.Writer
	// Redactor keeps the provider credential out of events and the session log. Its zero value
	// redacts nothing, which is what a test with no credential wants.
	Redactor redact.Redactor //exhaustruct:optional

	// Log records the conversation. Nil records nothing, which only a test without a broker
	// process may choose.
	Log core.SessionLog //exhaustruct:optional
	// Memory, Commands and Pages back the memory, exec and fetch tools; a nil one leaves its
	// tool registered but reporting itself unavailable, so the model is told rather than the
	// turn failing.
	Memory   core.MemoryStore   //exhaustruct:optional
	Commands core.CommandRunner //exhaustruct:optional
	Pages    core.PageFetcher   //exhaustruct:optional
	// Questioner and Approver reach the human. Nil means nobody can answer, which is the
	// fail-closed reading: ask_user cannot ask and anything needing approval is refused.
	Questioner core.Questioner //exhaustruct:optional
	Approver   core.Approver   //exhaustruct:optional
	// MCPPolicy decides MCP server launches. Its zero value grants nothing.
	MCPPolicy policy.Policy //exhaustruct:optional
	// MCPSandboxing confines MCP servers. Its zero value has no launcher, so every server is refused.
	MCPSandboxing mcp.Sandboxing //exhaustruct:optional
	// IsSpeechEnabled asks the model to mark what to say; something must be listening on Bus to say it.
	IsSpeechEnabled bool //exhaustruct:optional
}

// validate reports the first required dependency that is missing. A struct of dependencies
// trades the compiler's "not enough arguments" for a nil at run time, so it is checked here,
// once, before anything is built from it.
func (d chatSessionDeps) validate() error {
	for _, required := range []struct {
		name      string
		isPresent bool
	}{
		{"cfg", d.Cfg != nil},
		{"workspace dir", d.Paths.WorkspaceDir != ""},
		{"state dir", d.Paths.StateDir != ""},
		{"session id", d.Session != ""},
		{"provider", d.Provider != nil},
		{"model", d.Model != ""},
		{"file broker", d.Files != nil},
		{"event bus", d.Bus != nil},
		{"diagnostics", d.Diagnostics != nil},
	} {
		if !required.isPresent {
			return fmt.Errorf("a chat session needs a %s: %w", required.name, core.ErrInvalid)
		}
	}
	return nil
}

// assembleChatSession wires the registry, built-in tools, bus, trace and conversation for
// one session. Its dependencies are injected so the end-to-end test can script the model
// while everything else stays real.
func assembleChatSession(deps chatSessionDeps) (*chatSession, error) {
	if err := deps.validate(); err != nil {
		return nil, err
	}
	cfg, paths := deps.Cfg, deps.Paths
	clock := core.SystemClock{}
	toolRegistry := registry.BuildRegistry(cfg.Tools.MaxExposed)
	skillsCatalog := skills.BuildCatalog(cfg.Skills, paths, deps.Diagnostics, deps.Approver)
	isInstructionPath := skillsCatalog.ReportIsWorkspaceInstructionPath
	for _, tool := range []core.Tool{
		builtin.BuildFileReadTool(deps.Files, paths.WorkspaceDir, defaultMaxReadBytes, isInstructionPath),
		builtin.BuildGrepTool(deps.Files, paths.WorkspaceDir, isInstructionPath),
		builtin.BuildFileWriteTool(deps.Files, paths.WorkspaceDir, defaultMaxWriteBytes, config.ReportNativeLineEnding()),
		builtin.BuildFileEditTool(deps.Files, paths.WorkspaceDir, defaultMaxWriteBytes),
		builtin.BuildAskUserTool(deps.Questioner),
		builtin.BuildExecTool(deps.Commands),
		builtin.BuildFetchTool(deps.Pages),
		builtin.BuildMemoryTool(deps.Memory, filepath.Join(paths.StateDir, memory.FileName)),
	} {
		if err := toolRegistry.Register(registry.SourceBuiltin, tool); err != nil {
			return nil, err
		}
	}

	if err := toolRegistry.Register(registry.SourceSkill, skills.BuildOpenTool(skillsCatalog)); err != nil {
		return nil, err
	}

	// Async by design (BuildManager's own doc comment): connecting here, before the TUI's
	// event loop starts reading approvals, would deadlock any server that needs Ask.
	redactedEvents := redact.WrapSink(deps.Bus, deps.Redactor)
	mcpManager := mcp.BuildManager(cfg.MCP, deps.MCPPolicy, deps.Approver, clock, redactedEvents, deps.Diagnostics, toolRegistry, paths.StateDir, deps.MCPSandboxing)

	ids := id.BuildULIDGenerator(clock)
	loopDeps := loop.Dependencies{Provider: deps.Provider, Tools: toolRegistry, Events: redactedEvents, Clock: clock, IDs: ids, Skills: skillsCatalog, IsSpeechEnabled: deps.IsSpeechEnabled}
	if deps.Log != nil {
		loopDeps.Log = redact.WrapSessionLog(deps.Log, deps.Redactor)
	}

	session := &chatSession{
		conversation:  loop.BuildAssistantConversation(loopDeps, deps.Model, cfg.Brain.MaxSteps),
		bus:           deps.Bus,
		closeMCP:      mcpManager.Close,
		mcpManager:    mcpManager,
		toolRegistry:  toolRegistry,
		skillsCatalog: skillsCatalog,
		memoryStore:   deps.Memory,
		waitForTrace:  func() {},
	}
	if cfg.Eval.Traces {
		session.waitForTrace = startSessionTrace(deps.Bus, filepath.Join(paths.StateDir, "traces"), deps.Session, deps.Diagnostics)
	}
	return session, nil
}

// close stops MCP supervision first, so no late event from a dying server tries to publish
// after the bus closes, then ends the session's event stream and waits for the trace to flush.
func (s *chatSession) close() {
	s.closeMCP()
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
