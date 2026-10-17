package mcp

import (
	"context"
	"fmt"
	"io"
	"path/filepath"
	"sync"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/Kzeezee/travis/internal/config"
	"github.com/Kzeezee/travis/internal/core"
	"github.com/Kzeezee/travis/internal/security/policy"
	"github.com/Kzeezee/travis/internal/tools/registry"
)

// Manager owns every configured server's connection for one chat session's lifetime.
// Close stops supervision for all of them; it does not kill child processes forcibly —
// CommandTransport's own Close (via each session, closed through ctx cancellation ending
// their Wait) asks them to exit by closing stdin first, same as the broker's shutdown.
type Manager struct {
	cancel context.CancelFunc
	done   sync.WaitGroup
}

// BuildManager starts connecting every configured server in the background and registers
// each one's tools into reg as they come online — it never blocks the caller. This matters
// for a reason that is not optional: a server needing approval (policy Ask) can only be
// answered once the frontend's event loop is reading from approver's channel, and that loop
// does not start until the caller that constructed us returns. Connecting synchronously
// here would deadlock the whole session before the TUI ever draws a frame — it did, until
// this was made async. A server that fails to connect, list its tools, or register one is
// skipped with a diagnostic rather than failing the session. stateDir is where the shared
// trust store (mcp-trust.toml) lives — user-scoped, like the [mcp.servers] config itself —
// and where each server's scratch folder (mcp/<name>) is created.
func BuildManager(servers config.MCPCfg, pol policy.Policy, approver core.Approver, clock core.Clock, events core.EventSink, diagnostics io.Writer, reg *registry.Registry, stateDir string, sandboxing Sandboxing) *Manager {
	ctx, cancel := context.WithCancel(context.Background())
	manager := &Manager{cancel: cancel}

	trust := buildTrustStore(filepath.Join(stateDir, trustFileName))
	for name, serverCfg := range servers.Servers {
		process := buildServerProcess(name, serverCfg, pol, approver, clock, events, diagnostics, trust, sandboxing, filepath.Join(stateDir, "mcp", name), buildUnregisterTools(reg, name))
		manager.done.Add(1)
		go func(name string, process *serverProcess) {
			defer manager.done.Done()
			connectAndRegister(ctx, name, process, reg, diagnostics)
		}(name, process)
	}
	return manager
}

// buildUnregisterTools reports a closure that removes every tool server has registered so
// far from reg. Tools are namespaced mcp_<server>_<tool>, so server's own tools can always
// be found again by name prefix alone — no separate bookkeeping of what got registered.
func buildUnregisterTools(reg *registry.Registry, server string) func() {
	return func() {
		owned := reg.List(registry.Filter{Source: registry.SourceMCP, NamePrefix: toolNamePrefix(server)})
		names := make([]string, len(owned))
		for i, tool := range owned {
			names[i] = tool.Name()
		}
		reg.Unregister(names...)
	}
}

func connectAndRegister(ctx context.Context, name string, process *serverProcess, reg *registry.Registry, diagnostics io.Writer) {
	session, err := process.connectAndSupervise(ctx)
	if err != nil {
		fmt.Fprintf(diagnostics, "travis: mcp server %q did not start: %v\n", name, err)
		return
	}
	listed, err := session.ListTools(ctx, &mcpsdk.ListToolsParams{})
	if err != nil {
		fmt.Fprintf(diagnostics, "travis: mcp server %q: listing tools failed: %v\n", name, err)
		return
	}
	for _, remoteTool := range listed.Tools {
		tool := buildAdaptedTool(process, name, remoteTool)
		if err := reg.Register(registry.SourceMCP, tool); err != nil {
			fmt.Fprintf(diagnostics, "travis: mcp server %q: registering tool %q failed: %v\n", name, tool.Name(), err)
		}
	}
}

// Close ends supervision for every server this manager started and waits for their
// connect/register goroutines to notice and return, so nothing outlives the session. Call
// it once, when the chat session ends, mirroring broker.SessionBroker.Close's place in the
// shutdown order.
func (m *Manager) Close() {
	m.cancel()
	m.done.Wait()
}
