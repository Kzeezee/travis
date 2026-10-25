//go:build eval

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
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

// scriptedAnswers is the validated --answers file: approval decisions and ask_user replies,
// consumed in order.
type scriptedAnswers struct {
	approvals []core.ApprovalDecision
	questions []string
}

// scriptedApprovalDecisions are the only approvals a file may give; "always" is left out so
// one scripted answer can never widen policy for the rest of the run.
var scriptedApprovalDecisions = map[string]core.ApprovalDecision{"once": core.ApprovalOnce, "deny": core.ApprovalDenied}

// turnExitCodes is the exit-code contract with eval/inspect/travis_agent.py: 0 is end_turn,
// 1 is travis failing to run at all, and these are the other ways the loop ends a turn.
var turnExitCodes = map[core.StopReason]int{
	core.StopError:  2,
	core.StopBudget: 3,
	core.StopLength: 4,
}

// turnStoppedError reports a turn that ran but did not end with the model's answer.
type turnStoppedError struct {
	reason core.StopReason
	err    error
}

func (e turnStoppedError) Error() string {
	if e.err != nil {
		return fmt.Sprintf("turn stopped with %q: %v", e.reason, e.err)
	}
	return fmt.Sprintf("turn stopped with %q", e.reason)
}

func (e turnStoppedError) Unwrap() error { return e.err }

// ReportExitCode is how the turn stopped; a reason with no code of its own is an error.
func (e turnStoppedError) ReportExitCode() int {
	if code, ok := turnExitCodes[e.reason]; ok {
		return code
	}
	return turnExitCodes[core.StopError]
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
	if len(h.answers.approvals) > 0 {
		decision = h.answers.approvals[0]
		h.answers.approvals = h.answers.approvals[1:]
	}
	fmt.Fprintf(os.Stderr, "travis: approval %s %s -> %v\n", op.Kind, op.Target, decision)
	return decision, nil
}

func (h *scriptedHuman) AskUser(_ context.Context, question string) (string, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if len(h.answers.questions) == 0 {
		return "", fmt.Errorf("no scripted answer for %q: %w", question, core.ErrInvalid)
	}
	answer := h.answers.questions[0]
	h.answers.questions = h.answers.questions[1:]
	return answer, nil
}

// loadScriptedAnswers reads the --answers file strictly: a misspelt field or a second JSON
// value is refused rather than silently leaving the run with fewer answers than intended.
func loadScriptedAnswers(path string) (scriptedAnswers, error) {
	if path == "" {
		return scriptedAnswers{}, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return scriptedAnswers{}, err
	}
	var file struct {
		Approvals []string `json:"approvals"`
		Questions []string `json:"questions"`
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&file); err != nil {
		return scriptedAnswers{}, fmt.Errorf("answers file %s: %w: %w", path, err, core.ErrInvalid)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return scriptedAnswers{}, fmt.Errorf("answers file %s: holds more than one JSON value: %w", path, core.ErrInvalid)
	}
	answers := scriptedAnswers{questions: file.Questions}
	for _, approval := range file.Approvals {
		decision, ok := scriptedApprovalDecisions[approval]
		if !ok {
			return scriptedAnswers{}, fmt.Errorf("answers file %s: approval %q is not once or deny: %w", path, approval, core.ErrInvalid)
		}
		answers.approvals = append(answers.approvals, decision)
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
	apiKey, err := config.ResolveProviderAPIKey(cfg.Provider.Active, profile.Auth)
	if err != nil {
		return err
	}
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
	if turnErr != nil || reason != core.StopEndTurn {
		return turnStoppedError{reason: reason, err: turnErr}
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
