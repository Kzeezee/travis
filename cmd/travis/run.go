package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"sync"

	"github.com/Kzeezee/travis/internal/config"
	"github.com/Kzeezee/travis/internal/core"
	"github.com/Kzeezee/travis/internal/id"
	"github.com/Kzeezee/travis/internal/providers/openaicompat"
	"github.com/Kzeezee/travis/internal/security/broker"
	"github.com/Kzeezee/travis/internal/security/redact"
	"github.com/Kzeezee/travis/internal/tools/mcp"
)

// scriptedAnswers is the --answers file: approvals and ask_user replies, consumed in order.
type scriptedAnswers struct {
	Approvals []string `json:"approvals"`
	Questions []string `json:"questions"`
}

// scriptedHuman stands in for the keyboard in an eval run (6-spike). Once a list runs out it
// denies or answers nothing, the same fail-closed reading as having no human at all.
type scriptedHuman struct {
	mu      sync.Mutex
	answers scriptedAnswers
}

func (h *scriptedHuman) RequestApproval(_ context.Context, op core.Operation) (core.ApprovalDecision, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	decision := core.ApprovalDenied
	if len(h.answers.Approvals) > 0 {
		if h.answers.Approvals[0] == "once" {
			decision = core.ApprovalOnce
		}
		h.answers.Approvals = h.answers.Approvals[1:]
	}
	fmt.Fprintf(os.Stderr, "travis: approval %s %s -> %v\n", op.Kind, op.Target, decision)
	return decision, nil
}

func (h *scriptedHuman) AskUser(_ context.Context, question string) (string, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if len(h.answers.Questions) == 0 {
		return "", fmt.Errorf("no scripted answer for %q: %w", question, core.ErrInvalid)
	}
	answer := h.answers.Questions[0]
	h.answers.Questions = h.answers.Questions[1:]
	return answer, nil
}

func loadScriptedAnswers(path string) (scriptedAnswers, error) {
	var answers scriptedAnswers
	if path == "" {
		return answers, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return answers, err
	}
	if err := json.Unmarshal(data, &answers); err != nil {
		return answers, fmt.Errorf("answers file %s: %w", path, err)
	}
	for _, approval := range answers.Approvals {
		if approval != "once" && approval != "deny" {
			return answers, fmt.Errorf("answers file %s: approval %q is not once or deny: %w", path, approval, core.ErrInvalid)
		}
	}
	return answers, nil
}

// runHeadless runs one prompt to completion with no terminal, for the eval harness.
// shortcut: duplicates runChat's setup minus voice and the TUI; 6B should share one assembly.
func runHeadless(args []string, out io.Writer) error {
	flags := flag.NewFlagSet("run", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	prompt := flags.String("prompt", "", "the one prompt to run")
	answersPath := flags.String("answers", "", "JSON file of scripted approvals and ask_user answers")
	baseURL := flags.String("base-url", "", "override the active provider profile's endpoint base URL")
	model := flags.String("model", "", "override the active provider profile's model name")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *prompt == "" {
		return fmt.Errorf("run needs --prompt: %w", core.ErrInvalid)
	}
	answers, err := loadScriptedAnswers(*answersPath)
	if err != nil {
		return err
	}
	human := &scriptedHuman{answers: answers}

	cfg, diags, err := config.Load()
	for _, diag := range diags {
		fmt.Fprintf(os.Stderr, "travis: config: %s\n", diag.Message)
	}
	if err != nil {
		return err
	}
	profile := cfg.Provider.Profiles[cfg.Provider.Active]
	if *baseURL != "" {
		profile.BaseURL = *baseURL
	}
	if *model != "" {
		profile.Model = *model
	}

	paths, err := config.BuildCurrentPathSet()
	if err != nil {
		return err
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	mcpPolicy, err := buildMCPPolicy(cfg, paths, home)
	if err != nil {
		return err
	}
	apiKey := config.ResolveProviderAPIKey(profile.Auth)
	redactor := redact.New(apiKey)
	bus := buildSessionBus(os.Stderr)
	sessionID := id.BuildULIDGenerator(core.SystemClock{}).GenerateID()
	sessionBroker, err := startSessionBroker(paths, sessionID, cfg.Session, os.Stderr, broker.RemoteOptions{
		Events:   redact.WrapSink(bus, redactor),
		Approver: human,
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
		Questioner: human, Approver: human, MCPPolicy: mcpPolicy,
		MCPSandboxing: mcp.Sandboxing{
			ResolveLauncher: func() (string, error) { return broker.ResolveVerifiedBrokerExecutable(brokerSHA256) },
			IsRequired:      cfg.Security.RequireSandbox,
		},
	})
	if err != nil {
		return err
	}

	events, unsubscribe := session.bus.Subscribe(nil)
	outcome := make(chan core.StopReason, 1)
	go func() { outcome <- printHeadlessEvents(events, out) }()

	turnErr := session.conversation.RunAssistantTurn(context.Background(), *prompt)
	unsubscribe()
	session.close()
	reason := <-outcome
	if turnErr != nil {
		return turnErr
	}
	if reason != core.StopEndTurn {
		return fmt.Errorf("turn stopped with %q", reason)
	}
	return nil
}

// printHeadlessEvents writes the assistant's text to out and reports why the turn stopped.
func printHeadlessEvents(events <-chan core.Event, out io.Writer) core.StopReason {
	reason := core.StopError
	for event := range events {
		switch e := event.(type) {
		case core.AssistantTextStreamed:
			fmt.Fprint(out, e.Text)
		case core.ToolCallStarted:
			fmt.Fprintf(os.Stderr, "travis: tool %s\n", e.Call.Name)
		case core.TurnCompleted:
			reason = e.Reason
			fmt.Fprintln(out)
		}
	}
	return reason
}
