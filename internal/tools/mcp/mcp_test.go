package mcp

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Kzeezee/travis/internal/config"
	"github.com/Kzeezee/travis/internal/core"
	"github.com/Kzeezee/travis/internal/security/capabilities"
	"github.com/Kzeezee/travis/internal/security/policy"
)

// fakeServerPath is set once by TestMain to a real fakeserver binary built beside this test
// binary, mirroring cmd/travis/broker_test.go's pattern of compiling a real sibling process
// instead of mocking exec.Command.
var fakeServerPath string

func TestMain(m *testing.M) {
	path, err := buildFakeServerBesideTestBinary()
	if err != nil {
		fmt.Fprintln(os.Stderr, "internal/tools/mcp tests:", err)
		os.Exit(1)
	}
	fakeServerPath = path
	os.Exit(m.Run())
}

func buildFakeServerBesideTestBinary() (string, error) {
	testBinary, err := os.Executable()
	if err != nil {
		return "", err
	}
	name := "fakeserver"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	goTool, err := exec.LookPath("go")
	if err != nil {
		return "", fmt.Errorf("the go tool is needed to build fakeserver for these tests: %w", err)
	}
	serverPath := filepath.Join(filepath.Dir(testBinary), name)
	build := exec.CommandContext(context.Background(), goTool, "build", "-o", serverPath, "github.com/Kzeezee/travis/internal/tools/mcp/testdata/fakeserver")
	if output, err := build.CombinedOutput(); err != nil {
		return "", fmt.Errorf("build fakeserver: %w\n%s", err, output)
	}
	return serverPath, nil
}

// grantedExecPolicy evaluates to Ask for a CapabilityExec request: policy.go treats exec as
// irreversible and always escalates once a grant exists (D-27), it never silently Allows —
// so every test that connects to a real server must drive a fakeApprover, there is no
// grant that skips approval. Scope is "**" (matches across path segments), not "*" (one
// segment only), because tests configure servers by this fake binary's full built path,
// not a bare command name like a real config's "npx" would be.
func grantedExecPolicy() policy.Policy {
	grants := capabilities.Set{{Kind: core.CapabilityExec, Scope: "**"}}
	return policy.New(grants, nil, nil, false, "")
}

// deniedExecPolicy evaluates to Deny for every request: no grant covers it at all, which
// policy.go treats as Deny outright, never Ask.
func deniedExecPolicy() policy.Policy {
	return policy.New(nil, nil, nil, false, "")
}

// fakeApprover scripts one canned answer, proving the approval seam is reached without a
// real terminal. BuildManager connects every server on its own goroutine (that's the whole
// point of the deadlock fix elsewhere in this package), so a test polling askedOps() from
// its own goroutine while a connect is still in flight is a real concurrent access — asked
// must be mutex-guarded, not a plain slice.
type fakeApprover struct {
	decision core.ApprovalDecision

	mu    sync.Mutex
	asked []core.Operation
}

func (a *fakeApprover) RequestApproval(_ context.Context, op core.Operation) (core.ApprovalDecision, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.asked = append(a.asked, op)
	return a.decision, nil
}

// askedOps returns a snapshot of every Operation requested so far; always use this instead
// of reading a.asked directly, even from a test that looks single-goroutine today.
func (a *fakeApprover) askedOps() []core.Operation {
	a.mu.Lock()
	defer a.mu.Unlock()
	return slices.Clone(a.asked)
}

func buildTestServerConfig(args ...string) config.MCPServerCfg {
	return config.MCPServerCfg{Command: fakeServerPath, Args: args}
}

// syncBuffer is a thread-safe io.Writer + String(), for tests that pass a diagnostics
// sink to BuildManager (whose connect goroutines write to it) and then poll its contents
// from the test's own goroutine — a plain *strings.Builder is unsafe for exactly that.
type syncBuffer struct {
	mu  sync.Mutex
	buf strings.Builder
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// buildTestProcess is every test's entry point for a serverProcess wired to the real
// fakeserver binary, with a FixedClock so restart backoff never actually waits and a
// fresh trustStore backed by a temp directory, so trust tests never touch the real
// state dir or leak between tests. diagnostics is a syncBuffer, not a plain
// *strings.Builder: superviseRestarts writes to it from its own background goroutine
// (TestServerProcessRestartsAfterCrash exercises exactly this), and cancelling that
// goroutine's context doesn't synchronously stop an in-flight write, so the Cleanup
// below reading it back is a real concurrent access, same shape as the races CI caught
// elsewhere in this file.
func buildTestProcess(t *testing.T, cfg config.MCPServerCfg, pol policy.Policy, approver core.Approver) (*serverProcess, core.Clock) {
	t.Helper()
	diagnostics := &syncBuffer{}
	t.Cleanup(func() {
		if testing.Verbose() {
			t.Log(diagnostics.String())
		}
	})
	clock := core.FixedClock{Time: time.Now()}
	trust := buildTrustStore(filepath.Join(t.TempDir(), trustFileName))
	process := buildServerProcess("test-server", cfg, pol, approver, clock, nil, diagnostics, trust)
	return process, clock
}
