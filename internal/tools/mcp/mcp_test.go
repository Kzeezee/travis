package mcp

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
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
// real terminal.
type fakeApprover struct {
	decision core.ApprovalDecision
	asked    []core.Operation
}

func (a *fakeApprover) RequestApproval(_ context.Context, op core.Operation) (core.ApprovalDecision, error) {
	a.asked = append(a.asked, op)
	return a.decision, nil
}

func buildTestServerConfig(args ...string) config.MCPServerCfg {
	return config.MCPServerCfg{Command: fakeServerPath, Args: args}
}

// buildTestProcess is every test's entry point for a serverProcess wired to the real
// fakeserver binary, with a FixedClock so restart backoff never actually waits and a
// fresh trustStore backed by a temp directory, so trust tests never touch the real
// state dir or leak between tests.
func buildTestProcess(t *testing.T, cfg config.MCPServerCfg, pol policy.Policy, approver core.Approver) (*serverProcess, core.Clock) {
	t.Helper()
	var diagnostics strings.Builder
	t.Cleanup(func() {
		if testing.Verbose() {
			t.Log(diagnostics.String())
		}
	})
	clock := core.FixedClock{Time: time.Now()}
	trust := buildTrustStore(filepath.Join(t.TempDir(), trustFileName))
	process := buildServerProcess("test-server", cfg, pol, approver, clock, nil, &diagnostics, trust)
	return process, clock
}
