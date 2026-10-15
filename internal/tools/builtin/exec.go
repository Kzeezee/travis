package builtin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/Kzeezee/travis/internal/core"
)

const (
	execToolName    = "exec"
	execDescription = "Run a program in the workspace and report its output and exit status. " +
		"Pass the program and each argument as separate argv elements: there is no shell, so quoting, " +
		"globs, pipes, redirection and variables are not expanded and are passed through literally. " +
		"Every call needs the user's approval, however often the same command has been approved before."
	execSchema = `{"type":"object","properties":{` +
		`"argv":{"type":"array","items":{"type":"string"},"minItems":1,` +
		`"description":"The program followed by one element per argument, e.g. [\"git\",\"status\",\"--short\"]"}},` +
		`"required":["argv"],"additionalProperties":false}`
)

const (
	errorExecDenied   = "exec_denied"
	errorExecNotFound = "program_not_found"
	errorExecFailed   = "exec_failed"
)

// ExecTool asks the broker to run a program. It holds no execution logic of its own: the
// broker decides whether the command may run, runs it, and bounds its time and output.
type ExecTool struct {
	runner core.CommandRunner
}

type execArgs struct {
	Argv []string `json:"argv"`
}

func BuildExecTool(runner core.CommandRunner) *ExecTool {
	return &ExecTool{runner: runner}
}

func (t *ExecTool) Name() string { return execToolName }

func (t *ExecTool) Describe() core.ToolSpec {
	return core.ToolSpec{
		Name:        execToolName,
		Description: execDescription,
		InputSchema: json.RawMessage(execSchema),
		// The scope is whatever the user's security.exec_allow grants cover; only the broker
		// knows them, and it is the broker that enforces them.
		Capabilities: []core.Capability{{Kind: core.CapabilityExec, Scope: "*"}},
	}
}

func (t *ExecTool) Invoke(ctx context.Context, arguments json.RawMessage) (core.ToolOutput, error) {
	if err := ctx.Err(); err != nil {
		return core.ToolOutput{}, err
	}
	var args execArgs
	if err := decodeStrictArgs(arguments, &args); err != nil {
		return buildErrorOutput(errorInvalidArguments, map[string]any{"detail": err.Error()}), nil
	}
	if len(args.Argv) == 0 || strings.TrimSpace(args.Argv[0]) == "" {
		return buildErrorOutput(errorInvalidArguments, map[string]any{"detail": "argv must start with a program to run"}), nil
	}
	if t.runner == nil {
		return buildErrorOutput("exec_unavailable", nil), nil
	}

	result, err := t.runner.RunCommand(ctx, args.Argv)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return core.ToolOutput{}, ctxErr
		}
		return buildExecErrorOutput(args.Argv[0], err), nil
	}
	return describeCommandResult(args.Argv, result), nil
}

func buildExecErrorOutput(program string, err error) core.ToolOutput {
	fields := map[string]any{"program": program, "detail": err.Error()}
	switch {
	case errors.Is(err, core.ErrDenied):
		return buildErrorOutput(errorExecDenied, fields)
	case errors.Is(err, core.ErrNotFound):
		return buildErrorOutput(errorExecNotFound, fields)
	case errors.Is(err, core.ErrInvalid):
		return buildErrorOutput(errorInvalidArguments, fields)
	default:
		return buildErrorOutput(errorExecFailed, fields)
	}
}

// describeCommandResult reports the exit status as travis's own trusted line and the command's
// own bytes as untrusted: a program's output is no more vouched for than a fetched page, and a
// non-zero exit is a result the model acts on rather than an error.
func describeCommandResult(argv []string, result core.CommandResult) core.ToolOutput {
	status := fmt.Sprintf("exit status %d", result.ExitCode)
	if result.IsTimedOut {
		status = "timed out and was killed"
	}
	if result.IsOutputTruncated {
		status += "; output truncated at the broker's cap"
	}
	output := result.Output
	if strings.TrimSpace(output) == "" {
		output = "(no output)"
	}
	return core.ToolOutput{
		Content:     status + "\n" + core.WrapUntrustedContent("exec:"+argv[0], output),
		Display:     strings.Join(argv, " ") + " — " + status,
		IsError:     result.ExitCode != 0 || result.IsTimedOut,
		IsUntrusted: true,
	}
}
