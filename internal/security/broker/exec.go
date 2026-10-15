package broker

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"time"

	"github.com/Kzeezee/travis/internal/config"
	"github.com/Kzeezee/travis/internal/core"
)

const (
	// commandTimeout is how long one command may run (DESIGN §5.6 timeout). The client allows
	// a run_command exchange longer than its usual one for exactly this reason; the two
	// timeouts are a pair, so neither moves alone.
	commandTimeout = 120 * time.Second
	// commandWaitDelay bounds how long a killed command's output pipes are waited on, so a
	// grandchild that outlives its parent cannot hold the broker past the timeout.
	commandWaitDelay = 2 * time.Second
)

var _ core.CommandRunner = (*PolicyEnforcedBroker)(nil)

// RunCommand runs argv in the workspace once policy and a human allow it. Policy is evaluated
// on argv[0] as the model wrote it, the same way an MCP server launch is evaluated on the
// command the user wrote in their config, so an exec_allow entry means that exact spelling and
// nothing it might resolve to.
//
// Nothing about argv reaches a shell, so a denial cannot be talked around with quoting: the
// program is started directly and every other element is one argument to it.
func (b *PolicyEnforcedBroker) RunCommand(ctx context.Context, argv []string) (core.CommandResult, error) {
	if err := ctx.Err(); err != nil {
		return core.CommandResult{}, err
	}
	if err := validateCommandArgv(argv); err != nil {
		return core.CommandResult{}, err
	}
	// Authorization is settled before any process exists, so a refusal has nothing to undo.
	if err := b.authorize(ctx, core.CapabilityExec, argv[0]); err != nil {
		b.approvals.settleOnce(err)
		return core.CommandResult{}, err
	}
	result, err := b.runApprovedCommand(ctx, argv)
	b.approvals.settleOnce(err)
	return result, err
}

func (b *PolicyEnforcedBroker) runApprovedCommand(ctx context.Context, argv []string) (core.CommandResult, error) {
	runCtx, cancel := context.WithTimeout(ctx, commandTimeout)
	defer cancel()

	output := &cappedBuffer{limit: MaxCommandOutputBytes}
	command := exec.CommandContext(runCtx, argv[0], argv[1:]...) //nolint:gosec // argv is what policy authorized and a human approved; there is no shell
	command.Dir = b.resolvedRoot
	// The provider credential is travis's, not a subprocess's (§5.12.3).
	command.Env = config.BuildEnvironmentWithoutSecrets()
	command.Stdout, command.Stderr = output, output
	command.WaitDelay = commandWaitDelay

	// A command that resolved to the workspace itself (cwd is on PATH, as on Windows) arrives
	// here as exec.ErrDot; running it would let a file the model just wrote stand in for an
	// approved program name, so it stays refused.
	err := command.Run()
	result := core.CommandResult{
		Output: output.String(), IsOutputTruncated: output.isTruncated,
		IsTimedOut: errors.Is(runCtx.Err(), context.DeadlineExceeded),
	}
	var exited *exec.ExitError
	switch {
	case err == nil:
	case errors.As(err, &exited):
		result.ExitCode = exited.ExitCode()
	case result.IsTimedOut:
		result.ExitCode = -1
	default:
		return core.CommandResult{}, translateCommandError(argv[0], err)
	}
	return result, nil
}

// translateCommandError classifies a command that never ran, so the model is told whether the
// program is missing, refused, or something else entirely.
func translateCommandError(program string, err error) error {
	switch {
	case errors.Is(err, exec.ErrNotFound):
		return fmt.Errorf("%q was not found: %w", program, errors.Join(core.ErrNotFound, err))
	case errors.Is(err, exec.ErrDot):
		return fmt.Errorf("%q resolves inside the workspace, which is not an approved program: %w", program, errors.Join(core.ErrDenied, err))
	default:
		return fmt.Errorf("%q could not run: %w", program, err)
	}
}

// cappedBuffer collects a command's output up to limit and counts the rest as truncated, so a
// command that never stops printing cannot grow the broker's memory.
type cappedBuffer struct {
	collected   []byte
	limit       int
	isTruncated bool
}

func (b *cappedBuffer) Write(p []byte) (int, error) {
	if room := b.limit - len(b.collected); room > 0 {
		if len(p) <= room {
			b.collected = append(b.collected, p...)
			return len(p), nil
		}
		b.collected = append(b.collected, p[:room]...)
	}
	b.isTruncated = true
	// The whole write is reported as accepted: the child is not the party to tell about a cap
	// travis chose, and a short write would only make it fail with EPIPE instead.
	return len(p), nil
}

func (b *cappedBuffer) String() string { return string(b.collected) }
