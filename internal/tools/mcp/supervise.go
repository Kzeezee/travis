// Package mcp is the generic MCP stdio client (D-02): one implementation, zero per-server
// Go code. Every configured server becomes a core.Tool per tool it exposes, through the
// same tools/registry path builtin tools use. Every server launches inside the OS sandbox
// (internal/security/sandbox), confined to its runner and its own scratch folder.
package mcp

import (
	"context"
	"fmt"
	"io"
	"os/exec"
	"runtime"
	"sync"
	"time"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/Kzeezee/travis/internal/config"
	"github.com/Kzeezee/travis/internal/core"
	"github.com/Kzeezee/travis/internal/security/policy"
	"github.com/Kzeezee/travis/internal/security/sandbox"
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

// serverProcess owns launches and restarts; remembered MCP trust never overrides a denied grant.
type serverProcess struct {
	name        string
	cfg         config.MCPServerCfg
	policy      policy.Policy
	approver    core.Approver
	clock       core.Clock
	events      core.EventSink
	diagnostics io.Writer
	trust       *trustStore
	sandboxing  Sandboxing
	// buildSandboxedCommand is sandbox.BuildCommand; tests replace it to stand in for an OS without a sandbox.
	buildSandboxedCommand func(context.Context, sandbox.Spec) (*exec.Cmd, sandbox.Tier, error)
	// scratchDir is this server's only writable folder by default; its home, temp and caches live there.
	scratchDir string
	// unregisterTools removes this server's own tools from the registry once restart
	// attempts are exhausted; nil in tests that never reach exhaustion.
	unregisterTools func()

	mu          sync.Mutex
	session     *mcpsdk.ClientSession
	unavailable error
}

func buildServerProcess(name string, cfg config.MCPServerCfg, pol policy.Policy, approver core.Approver, clock core.Clock, events core.EventSink, diagnostics io.Writer, trust *trustStore, sandboxing Sandboxing, scratchDir string, unregisterTools func()) *serverProcess {
	return &serverProcess{name: name, cfg: cfg, policy: pol, approver: approver, clock: clock, events: events, diagnostics: diagnostics, trust: trust, sandboxing: sandboxing, buildSandboxedCommand: sandbox.BuildCommand, scratchDir: scratchDir, unregisterTools: unregisterTools}
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

// connect builds the sandboxed command, authorizes it, then completes the MCP handshake over it.
// The tier is known before approval, so the human approves the launch as it will actually run.
// Policy is evaluated against the configured command name, not its OS-resolved variant:
// the user wrote "npx" in their config, so their grant should match "npx", not a
// platform-specific "npx.cmd" or an absolute path that happens to vary by machine.
func (p *serverProcess) connect(ctx context.Context) (*mcpsdk.ClientSession, error) {
	cmd, grant, err := p.buildLaunchCommand(ctx)
	if err != nil {
		return nil, err
	}
	if err := p.authorizeExec(ctx, p.cfg.Command, grant); err != nil {
		return nil, err
	}
	// A server that dies at startup says why only on stderr (an npm network error, a sandbox denial). It goes to a
	// file, not the terminal, where a chatty server's log lines would draw over the chat screen.
	stderrLog, err := createStderrLog(p.scratchDir)
	if err != nil {
		return nil, fmt.Errorf("mcp server %q: %w", p.name, err)
	}
	defer stderrLog.Close() // the child holds its own copy once started
	cmd.Stderr = stderrLog

	client := mcpsdk.NewClient(&mcpsdk.Implementation{Name: "travis", Version: clientVersion}, nil)
	session, err := client.Connect(ctx, &mcpsdk.CommandTransport{Command: cmd}, nil)
	if err != nil {
		fmt.Fprintf(p.diagnostics, "travis: mcp server %q wrote its startup errors to %s\n", p.name, stderrLog.Name())
		return nil, translateConnectError(p.name, err)
	}
	p.publish(ctx, core.MCPServerStarted{EventHeader: core.EventHeader{At: p.clock.ReportCurrentTime()}, Server: p.name, Tier: string(grant.Tier)})
	return session, nil
}

// authorizeExec checks current grants before remembered approval, so revoked grants always deny.
func (p *serverProcess) authorizeExec(ctx context.Context, command string, grant launchGrant) error {
	decision := p.policy.Evaluate(ctx, policy.Request{Kind: core.CapabilityExec, Target: command})
	switch decision.Verdict {
	case policy.Allow:
		return nil
	case policy.Ask:
		if p.trust != nil && p.trust.isTrusted(command, p.cfg.Args, grant) {
			return nil
		}
		return p.requestApproval(ctx, command, grant)
	case policy.Deny:
		return fmt.Errorf("mcp server %q is not permitted to run %q; add it to [security] exec_allow in your user config: %w", p.name, command, core.ErrDenied)
	default:
		return fmt.Errorf("mcp server %q is not permitted to run %q: %w", p.name, command, core.ErrDenied)
	}
}

func (p *serverProcess) requestApproval(ctx context.Context, command string, grant launchGrant) error {
	if p.approver == nil {
		return fmt.Errorf("starting mcp server %q needs approval, but none is available: %w", p.name, core.ErrDenied)
	}
	// Not IsIrreversible: unlike generic exec, this operation is scoped to the exact
	// (command, args) the user already wrote into their own trusted config — approving
	// it "always" is safe to offer, because trust.add below ties the answer to that
	// exact content, not to exec in general (D-27 stays untouched for anything else).
	op := core.Operation{
		Kind:        core.CapabilityExec,
		Target:      command,
		Explanation: fmt.Sprintf("start local MCP server %q (%s), sandbox: %s, network: %s", p.name, command, grant.Tier, reportNetworkAccess(grant.AllowNetwork)),
	}
	decision, err := p.approver.RequestApproval(ctx, op)
	if err != nil {
		return err
	}
	switch decision {
	case core.ApprovalOnce:
		return nil
	case core.ApprovalAlways:
		if p.trust == nil {
			return nil
		}
		return p.trust.add(command, p.cfg.Args, grant, "approval modal", p.clock)
	case core.ApprovalDenied:
		return fmt.Errorf("starting mcp server %q was denied by the user: %w", p.name, core.ErrDenied)
	default:
		return fmt.Errorf("starting mcp server %q was denied by the user: %w", p.name, core.ErrDenied)
	}
}

func reportNetworkAccess(isAllowed bool) string {
	if isAllowed {
		return "allowed"
	}
	return "blocked"
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
	if p.unregisterTools != nil {
		p.unregisterTools()
	}
	p.publish(ctx, core.MCPServerUnavailable{
		EventHeader: core.EventHeader{At: p.clock.ReportCurrentTime()},
		Server:      p.name,
		Reason:      reason,
	})
}

func (p *serverProcess) publish(ctx context.Context, event core.Event) {
	if p.events != nil {
		p.events.Publish(ctx, event)
	}
}
