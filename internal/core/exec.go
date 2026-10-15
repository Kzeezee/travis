package core

import "context"

// CommandRunner runs one program on a tool's behalf (D-18). argv is passed to the program
// directly: there is no shell, so nothing in it is expanded, split or substituted.
//
// It is a port of its own rather than a Broker method because only a policy-enforcing broker
// may run anything at all — an unenforced broker has no exec rule to confine a child process
// the way workspace confinement confines a path.
//
// The runner owns every choice about how the command runs: the working directory, an
// environment without the provider credential, a timeout and a cap on the output it collects.
// Running always needs a human's keypress, every time (D-27).
type CommandRunner interface {
	RunCommand(ctx context.Context, argv []string) (CommandResult, error)
}

// CommandResult is how a command that actually ran ended. A non-zero ExitCode is a normal
// result rather than an error: the model reads it and decides what to do next. A command that
// never started is an error wrapping ErrNotFound, ErrDenied or ErrInvalid instead.
type CommandResult struct {
	ExitCode int
	// Output is stdout and stderr together, in the order the child wrote them, which is what a
	// human would have seen in a terminal.
	Output string
	// IsOutputTruncated reports that the command wrote more than the runner's cap and the rest
	// was dropped; the command itself still ran to completion.
	IsOutputTruncated bool
	// IsTimedOut reports that the runner killed the command at its timeout, so Output is
	// whatever it had written by then.
	IsTimedOut bool
}
