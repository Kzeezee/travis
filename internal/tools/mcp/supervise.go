// Package mcp is the generic MCP stdio client (D-02): one implementation, zero per-server
// Go code. Every configured server becomes a core.Tool per tool it exposes, through the
// same tools/registry path builtin tools use. Trusted/local servers only — the public MCP
// ecosystem and its sandboxing wait for a later phase.
package mcp

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
	"sync"
	"time"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/Kzeezee/travis/internal/config"
	"github.com/Kzeezee/travis/internal/core"
	"github.com/Kzeezee/travis/internal/security/policy"
)

const (
	maxRestartAttempts = 3
	baseRestartDelay   = 500 * time.Millisecond
	clientVersion      = "0.1.0"
)

// resolveServerCommand reports the executable a configured command actually resolves to,
// handling Windows's .cmd-shim convention for npm-distributed launchers (npx, and anything
// else npm installs as a shim) — the one OS quirk this package owns (DESIGN.md §8.1).
func resolveServerCommand(command string) string {
	if runtime.GOOS != "windows" {
		return command
	}
	switch command {
	case "npx", "npm", "corepack":
		return command + ".cmd"
	default:
		return command
	}
}

// serverProcess owns one configured server's connection lifecycle: the exec-capability
// gate, the subprocess, and restart-with-backoff after a crash. Policy treats exec as
// irreversible and always escalates to Ask (policy.go: "exec is irreversible and always
// asks (D-27)"), and the approval modal refuses ApprovalAlways for an irreversible
// operation (§6.4) — so in practice every `travis chat` session re-asks to start each
// configured server; alwaysApproved exists only in case that D-27 stance is ever narrowed
// for a user's own already-configured servers, not because it fires today.
type serverProcess struct {
	name        string
	cfg         config.MCPServerCfg
	policy      policy.Policy
	approver    core.Approver
	clock       core.Clock
	events      core.EventSink
	diagnostics io.Writer

	mu             sync.Mutex
	alwaysApproved bool
	session        *mcpsdk.ClientSession
	unavailable    error
}

func buildServerProcess(name string, cfg config.MCPServerCfg, pol policy.Policy, approver core.Approver, clock core.Clock, events core.EventSink, diagnostics io.Writer) *serverProcess {
	return &serverProcess{name: name, cfg: cfg, policy: pol, approver: approver, clock: clock, events: events, diagnostics: diagnostics}
}

// connectAndSupervise performs the first connect (so tools/list has something to report)
// and, once it succeeds, starts restart-with-backoff supervision for the rest of the
// process's life. ctx outlives the call: it is the session's context, not just this call's.
func (p *serverProcess) connectAndSupervise(ctx context.Context) (*mcpsdk.ClientSession, error) {
	session, err := p.connect(ctx)
	if err != nil {
		return nil, err
	}
	p.mu.Lock()
	p.session = session
	p.mu.Unlock()
	p.superviseRestarts(ctx, session)
	return session, nil
}

// currentSession returns the live session, or the recorded failure once the server has
// exhausted its restart attempts and been reported unavailable.
func (p *serverProcess) currentSession() (*mcpsdk.ClientSession, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.unavailable != nil {
		return nil, p.unavailable
	}
	return p.session, nil
}

// connect authorizes and starts one subprocess, then completes the MCP handshake over it.
// Policy is evaluated against the configured command name, not its OS-resolved variant:
// the user wrote "npx" in their config, so their grant should match "npx", not a
// platform-specific "npx.cmd" or an absolute path that happens to vary by machine.
func (p *serverProcess) connect(ctx context.Context) (*mcpsdk.ClientSession, error) {
	if err := p.authorizeExec(ctx, p.cfg.Command); err != nil {
		return nil, err
	}

	// resolved and Args come only from the user's own trusted config, never from model output.
	resolved := resolveServerCommand(p.cfg.Command)
	cmd := exec.CommandContext(ctx, resolved, p.cfg.Args...)
	cmd.Env = buildServerEnv(p.cfg.Env)

	client := mcpsdk.NewClient(&mcpsdk.Implementation{Name: "travis", Version: clientVersion}, nil)
	session, err := client.Connect(ctx, &mcpsdk.CommandTransport{Command: cmd}, nil)
	if err != nil {
		return nil, translateConnectError(p.name, err)
	}
	return session, nil
}

// authorizeExec evaluates the exec capability for the configured command, asking a human
// when policy says Ask, and never starting anything policy says Deny.
func (p *serverProcess) authorizeExec(ctx context.Context, command string) error {
	p.mu.Lock()
	alwaysApproved := p.alwaysApproved
	p.mu.Unlock()
	if alwaysApproved {
		return nil
	}

	decision := p.policy.Evaluate(ctx, policy.Request{Kind: core.CapabilityExec, Target: command})
	switch decision.Verdict {
	case policy.Allow:
		return nil
	case policy.Ask:
		return p.requestApproval(ctx, command)
	case policy.Deny:
		return fmt.Errorf("mcp server %q is not permitted to run %q: %w", p.name, command, core.ErrDenied)
	default:
		return fmt.Errorf("mcp server %q is not permitted to run %q: %w", p.name, command, core.ErrDenied)
	}
}

func (p *serverProcess) requestApproval(ctx context.Context, command string) error {
	if p.approver == nil {
		return fmt.Errorf("starting mcp server %q needs approval, but none is available: %w", p.name, core.ErrDenied)
	}
	op := core.Operation{
		Kind:           core.CapabilityExec,
		Target:         command,
		Explanation:    fmt.Sprintf("start local MCP server %q (%s)", p.name, command),
		IsIrreversible: true,
	}
	decision, err := p.approver.RequestApproval(ctx, op)
	if err != nil {
		return err
	}
	switch decision {
	case core.ApprovalOnce:
		return nil
	case core.ApprovalAlways:
		p.mu.Lock()
		p.alwaysApproved = true
		p.mu.Unlock()
		return nil
	case core.ApprovalDenied:
		return fmt.Errorf("starting mcp server %q was denied by the user: %w", p.name, core.ErrDenied)
	default:
		return fmt.Errorf("starting mcp server %q was denied by the user: %w", p.name, core.ErrDenied)
	}
}

// buildServerEnv layers the server's configured env over travis's own, so a server still
// finds its runner (node, python, …) on PATH without the config needing to repeat it.
func buildServerEnv(configured map[string]string) []string {
	env := os.Environ()
	for key, value := range configured {
		env = append(env, key+"="+value)
	}
	return env
}

// superviseRestarts reconnects after an unexpected exit, with backoff, up to
// maxRestartAttempts; it gives up and reports Reason through events and diagnostics rather
// than retrying forever (DESIGN.md §5.7: "never silently retry forever").
func (p *serverProcess) superviseRestarts(ctx context.Context, session *mcpsdk.ClientSession) {
	go func() {
		for attempt := 1; ; attempt++ {
			exitErr := session.Wait()
			if ctx.Err() != nil {
				return
			}
			fmt.Fprintf(p.diagnostics, "travis: mcp server %q exited: %v\n", p.name, exitErr)
			if attempt > maxRestartAttempts {
				p.reportUnavailable(ctx, fmt.Sprintf("exited %d times, last error: %v", attempt-1, exitErr))
				return
			}

			select {
			case <-ctx.Done():
				return
			case <-p.clock.After(baseRestartDelay << attempt):
			}

			next, err := p.connect(ctx)
			if err != nil {
				fmt.Fprintf(p.diagnostics, "travis: mcp server %q restart %d failed: %v\n", p.name, attempt, err)
				continue
			}
			session = next
			p.mu.Lock()
			p.session = session
			p.mu.Unlock()
		}
	}()
}

func (p *serverProcess) reportUnavailable(ctx context.Context, reason string) {
	fmt.Fprintf(p.diagnostics, "travis: mcp server %q is unavailable: %s\n", p.name, reason)
	p.mu.Lock()
	p.unavailable = &core.ProviderError{Provider: sourceName(p.name), Kind: core.ErrExhausted, Cause: fmt.Errorf("%s", reason)}
	p.session = nil
	p.mu.Unlock()
	if p.events == nil {
		return
	}
	p.events.Publish(ctx, core.MCPServerUnavailable{
		EventHeader: core.EventHeader{At: p.clock.ReportCurrentTime()},
		Server:      p.name,
		Reason:      reason,
	})
}
