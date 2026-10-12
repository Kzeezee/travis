package terminal

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/Kzeezee/travis/internal/core"
)

// runCmd executes cmd and, for a batch, launches each inner command the way the real
// Bubble Tea runtime would: concurrently, since one of them (listenForApprovals) blocks
// until the next request arrives and must never stall the others.
func runCmd(cmd tea.Cmd) {
	if cmd == nil {
		return
	}
	if batch, isBatch := cmd().(tea.BatchMsg); isBatch {
		for _, inner := range batch {
			go runCmd(inner)
		}
	}
}

func buildPendingApproval(t *testing.T) (chatModel, chan core.ApprovalDecision) {
	t.Helper()
	answers := make(chan core.ApprovalDecision, 1)
	m := buildTestModel(func(context.Context, string) error { return nil })
	m.pendingApproval = &core.ApprovalRequest{
		Operation: core.Operation{Kind: core.CapabilityFSWrite, Target: "/w/notes.md", Explanation: "no grant covers it"},
		Respond:   func(d core.ApprovalDecision) { answers <- d },
	}
	return m, answers
}

func TestHandleApprovalKeyAnswersYOnceAAlwaysNDenied(t *testing.T) {
	tests := []struct {
		key  tea.KeyPressMsg
		want core.ApprovalDecision
	}{
		{tea.KeyPressMsg{Code: 'y', Text: "y"}, core.ApprovalOnce},
		{tea.KeyPressMsg{Code: 'a', Text: "a"}, core.ApprovalAlways},
		{tea.KeyPressMsg{Code: 'n', Text: "n"}, core.ApprovalDenied},
		{escKey, core.ApprovalDenied},
	}
	for _, tt := range tests {
		t.Run(tt.key.String(), func(t *testing.T) {
			m, answers := buildPendingApproval(t)
			next, cmd := updateModel(m, tt.key)
			if next.pendingApproval != nil {
				t.Fatal("want the pending approval cleared once answered")
			}
			if cmd == nil {
				t.Fatal("want a command that delivers the answer")
			}
			runCmd(cmd)

			select {
			case got := <-answers:
				if got != tt.want {
					t.Fatalf("got decision %v, want %v", got, tt.want)
				}
			case <-time.After(time.Second):
				t.Fatal("Respond was never called")
			}
		})
	}
}

func TestHandleApprovalKeySwallowsUnrecognisedKeys(t *testing.T) {
	m, answers := buildPendingApproval(t)
	next, cmd := updateModel(m, tea.KeyPressMsg{Code: 'z', Text: "z"})

	if next.pendingApproval == nil {
		t.Fatal("want the pending approval kept for an unrecognised key")
	}
	if cmd != nil {
		t.Fatal("want no command for an unrecognised key")
	}
	select {
	case got := <-answers:
		t.Fatalf("Respond must not be called for an unrecognised key, got %v", got)
	default:
	}
}

func TestPendingApprovalTakesEveryKeyBeforeTheInput(t *testing.T) {
	m, _ := buildPendingApproval(t)
	next, _ := updateModel(m, tea.KeyPressMsg{Code: 'y', Text: "y"})
	if next.input.Value() != "" {
		t.Fatalf("got input %q, want the approval key not to reach the input", next.input.Value())
	}
}

func TestChannelApproverDeliversTheRequestAndReturnsTheAnswer(t *testing.T) {
	requests := make(chan core.ApprovalRequest, 1)
	approver := BuildApprover(requests)
	op := core.Operation{Kind: core.CapabilityFSWrite, Target: "/w/notes.md"}

	result := make(chan core.ApprovalDecision, 1)
	errs := make(chan error, 1)
	go func() {
		decision, err := approver.RequestApproval(context.Background(), op)
		result <- decision
		errs <- err
	}()

	select {
	case request := <-requests:
		if request.Operation != op {
			t.Fatalf("got operation %+v, want %+v", request.Operation, op)
		}
		request.Respond(core.ApprovalAlways)
	case <-time.After(time.Second):
		t.Fatal("RequestApproval never sent a request")
	}

	if got := <-result; got != core.ApprovalAlways {
		t.Fatalf("got decision %v, want ApprovalAlways", got)
	}
	if err := <-errs; err != nil {
		t.Fatalf("got error %v, want nil", err)
	}
}

func TestViewShowsThePendingApprovalInPlaceOfTheInput(t *testing.T) {
	m, _ := buildPendingApproval(t)
	view := renderPlainView(m)
	if !strings.Contains(view, "fs_write") || !strings.Contains(view, "/w/notes.md") ||
		!strings.Contains(view, "no grant covers it") || !strings.Contains(view, "[y] once") ||
		!strings.Contains(view, "[a] always") || !strings.Contains(view, "[n] deny") {
		t.Fatalf("got view\n%s\nwant the pending operation, explanation, and the y/a/n legend", view)
	}
}

// TestViewKeepsTheLegendWholeEvenWithALongExplanation guards against truncating the whole
// modal line as one block: a long policy.Explain trace must not eat into, or hide, the
// [y]/[a]/[n] legend the keyboard actually depends on.
func TestViewKeepsTheLegendWholeEvenWithALongExplanation(t *testing.T) {
	m := buildTestModel(func(context.Context, string) error { return nil })
	m.pendingApproval = &core.ApprovalRequest{
		Operation: core.Operation{
			Kind:   core.CapabilityFSWrite,
			Target: "/workspace/notes/today.md",
			Explanation: "pass: fs_write \"/workspace/notes/today.md\" is resolved\n" +
				"pass: fs_write \"/workspace/notes/today.md\" matches no deny rule\n" +
				"deny: no fs_write grant covers \"/workspace/notes/today.md\"",
		},
		Respond: func(core.ApprovalDecision) {},
	}

	view := renderPlainView(m)
	if !strings.Contains(view, "[y] once · [a] always · [n] deny") {
		t.Fatalf("got view\n%s\nwant the full legend kept intact despite the long explanation", view)
	}
}

func TestChannelApproverReturnsOnContextCancellation(t *testing.T) {
	requests := make(chan core.ApprovalRequest) // unbuffered and never drained
	approver := BuildApprover(requests)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := approver.RequestApproval(ctx, core.Operation{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("got error %v, want context.Canceled", err)
	}
}

// §6.4: an irreversible write can be approved once, never always, so a stray a answers nothing.
func TestIrreversibleApprovalOffersNoAlways(t *testing.T) {
	m, answers := buildPendingApproval(t)
	m.pendingApproval.Operation.IsIrreversible = true

	view := renderPlainView(m)
	if strings.Contains(view, "[a] always") || !strings.Contains(view, "can't be undone · [y] once · [n] deny") {
		t.Fatalf("got view\n%s\nwant the legend without always, saying it can't be undone", view)
	}
	next, cmd := updateModel(m, tea.KeyPressMsg{Code: 'a', Text: "a"})
	if next.pendingApproval == nil || cmd != nil {
		t.Fatal("want a ignored while an irreversible approval is pending")
	}
	select {
	case got := <-answers:
		t.Fatalf("a answered an irreversible approval with %v", got)
	default:
	}
}
