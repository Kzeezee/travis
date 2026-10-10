// Command approvaldemo is a throwaway demo of the Phase 2/2D approval modal: nothing in
// the real app fires an ApprovalRequest yet (that's 2B's job), so this fires a fake one a
// couple of seconds after startup so the modal can be seen and driven by hand. Delete this
// directory once you're done looking at it; it is not part of the product.
package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/Kzeezee/travis/internal/core"
	"github.com/Kzeezee/travis/internal/frontends/terminal"
)

func main() {
	events := make(chan core.Event)
	approvals := make(chan core.ApprovalRequest)

	// No real turn to run; the demo is only about the modal.
	runTurn := func(ctx context.Context, prompt string) error { return nil }

	decisions := make(chan core.ApprovalDecision, 1)
	go func() {
		time.Sleep(2 * time.Second)
		approvals <- core.ApprovalRequest{
			Operation: core.Operation{
				Kind:        core.CapabilityFSWrite,
				Target:      "/workspace/notes/today.md",
				Explanation: "pass: fs_write \"/workspace/notes/today.md\" is resolved\npass: fs_write \"/workspace/notes/today.md\" matches no deny rule\ndeny: no fs_write grant covers \"/workspace/notes/today.md\"",
			},
			Respond: func(d core.ApprovalDecision) { decisions <- d },
		}
	}()

	err := terminal.RunChat(context.Background(), events, approvals, runTurn,
		terminal.Options{ModelName: "approvaldemo", Input: os.Stdin, Output: os.Stdout})

	select {
	case decision := <-decisions:
		fmt.Fprintf(os.Stderr, "approvaldemo: you answered %s\n", decision)
	default:
		fmt.Fprintln(os.Stderr, "approvaldemo: quit before answering")
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "approvaldemo:", err)
		os.Exit(1)
	}
}
