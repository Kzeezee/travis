package broker_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/Kzeezee/travis/internal/core"
	"github.com/Kzeezee/travis/internal/security/broker"
)

// startPipedBroker serves effects on one end of an in-memory pipe and returns a RemoteBroker
// on the other, plus a stop function that ends the server and waits for it.
func startPipedBroker(t *testing.T, effects core.Broker) (*broker.RemoteBroker, func() error) {
	t.Helper()
	return startPipedBrokerWith(t, effects, broker.RemoteOptions{})
}

func startPipedBrokerWith(t *testing.T, effects core.Broker, options broker.RemoteOptions) (*broker.RemoteBroker, func() error) {
	t.Helper()
	serverConn, clientConn := net.Pipe()
	ctx, cancel := context.WithCancel(context.Background())
	served := make(chan error, 1)
	go func() { served <- broker.ServeConnection(ctx, serverConn, effects) }()

	remote := broker.BuildRemoteBroker(clientConn, options)
	stop := sync.OnceValue(func() error {
		cancel()
		return <-served
	})
	t.Cleanup(func() {
		_ = remote.Close()
		_ = stop()
	})
	return remote, stop
}

func TestRemoteBrokerReportsTheSameAnswersAsTheBrokerItForwardsTo(t *testing.T) {
	enforced, workspace, _ := buildSessionBroker(t, "paranoid")
	writeWorkspaceFile(t, workspace, "notes/today.md", "hi")
	remote, _ := startPipedBroker(t, enforced)
	ctx := context.Background()

	want, err := enforced.DescribePath(ctx, "notes/today.md")
	if err != nil {
		t.Fatal(err)
	}
	if got, err := remote.DescribePath(ctx, "notes/today.md"); err != nil || got != want {
		t.Fatalf("DescribePath = %+v, %v; want %+v", got, err, want)
	}

	entries, err := remote.ListDirectory(ctx, "notes")
	if err != nil || len(entries) != 1 || entries[0] != want {
		t.Fatalf("ListDirectory = %+v, %v; want [%+v]", entries, err, want)
	}
}

func TestRemoteBrokerStreamsFilesLargerThanOneReadChunk(t *testing.T) {
	enforced, workspace, _ := buildSessionBroker(t, "paranoid")
	content := strings.Repeat("0123456789abcdef", (2*broker.MaxReadChunkBytes)/16+7)
	writeWorkspaceFile(t, workspace, "big.txt", content)
	remote, _ := startPipedBroker(t, enforced)

	opened, err := remote.OpenFile(context.Background(), "big.txt")
	if err != nil {
		t.Fatal(err)
	}
	got, err := io.ReadAll(opened.Content)
	if err != nil || !bytes.Equal(got, []byte(content)) {
		t.Fatalf("read %d bytes, err %v; want %d bytes", len(got), err, len(content))
	}
	if err := opened.Content.Close(); err != nil {
		t.Fatal(err)
	}
	if opened.Info.WorkspacePath != "big.txt" || opened.Info.Kind != core.PathKindFile {
		t.Fatalf("OpenFile info = %+v", opened.Info)
	}
}

func TestRemoteBrokerCarriesErrorKindsAcrossTheWire(t *testing.T) {
	enforced, workspace, _ := buildSessionBroker(t, "paranoid")
	writeWorkspaceFile(t, workspace, ".env", "TOKEN=secret")
	writeWorkspaceFile(t, workspace, "notes/today.md", "hi")
	remote, _ := startPipedBroker(t, enforced)

	for path, want := range map[string]error{
		".env":        core.ErrDenied,
		"missing.txt": core.ErrNotFound,
		"notes":       core.ErrIsDirectory,
		"bogus:x":     core.ErrInvalid,
	} {
		if _, err := remote.OpenFile(context.Background(), path); !errors.Is(err, want) {
			t.Errorf("OpenFile(%q) error = %v, want %v", path, err, want)
		}
	}
	if _, err := remote.DescribePath(context.Background(), "notes/today.md"); err != nil {
		t.Fatalf("a refused request broke the connection: %v", err)
	}
}

func TestRemoteBrokerFailsClosedOnceTheBrokerIsGone(t *testing.T) {
	enforced, workspace, _ := buildSessionBroker(t, "paranoid")
	writeWorkspaceFile(t, workspace, "notes/today.md", "hi")
	remote, stop := startPipedBroker(t, enforced)

	if err := stop(); err != nil {
		t.Fatalf("ServeConnection after cancel = %v, want nil", err)
	}
	for range 2 {
		if _, err := remote.OpenFile(context.Background(), "notes/today.md"); err == nil {
			t.Fatal("OpenFile succeeded with no broker behind it")
		}
	}
}

func TestRemoteBrokerHonoursAnAlreadyCancelledContext(t *testing.T) {
	enforced, _, _ := buildSessionBroker(t, "paranoid")
	remote, _ := startPipedBroker(t, enforced)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := remote.DescribePath(ctx, "."); !errors.Is(err, context.Canceled) {
		t.Fatalf("DescribePath(cancelled) error = %v, want context.Canceled", err)
	}
	if _, err := remote.DescribePath(context.Background(), "."); err != nil {
		t.Fatalf("a call cancelled before sending broke the connection: %v", err)
	}
}

func TestServeConnectionAnswersRefusedRequestsAndKeepsServing(t *testing.T) {
	serverConn, clientConn := net.Pipe()
	defer clientConn.Close()
	effects := broker.BuildLocalBroker(t.TempDir(), core.SystemClock{})
	served := make(chan error, 1)
	go func() { served <- broker.ServeConnection(context.Background(), serverConn, effects) }()

	exchange := func(req broker.Request) broker.Response {
		t.Helper()
		if err := broker.WriteRequest(clientConn, req); err != nil {
			t.Fatal(err)
		}
		resp, err := broker.ReadResponse(clientConn)
		if err != nil {
			t.Fatal(err)
		}
		return resp
	}

	for name, tc := range map[string]struct {
		req  broker.Request
		want broker.ErrorCode
	}{
		"unknown version": {broker.Request{Version: broker.ProtocolVersion + 1, ID: 1, Op: broker.OpDescribePath, Path: "."}, broker.ErrorUnsupportedVersion},
		"unknown op":      {broker.Request{Version: broker.ProtocolVersion, ID: 2, Op: "delete_everything"}, broker.ErrorInvalid},
		"unopened handle": {broker.Request{Version: broker.ProtocolVersion, ID: 3, Op: broker.OpReadFile, Handle: 9, MaxBytes: 1}, broker.ErrorInvalid},
	} {
		resp := exchange(tc.req)
		if resp.ID != tc.req.ID || resp.Error == nil || resp.Error.Code != tc.want {
			t.Errorf("%s: response %+v, want id %d and code %q", name, resp, tc.req.ID, tc.want)
		}
	}

	resp := exchange(broker.Request{Version: broker.ProtocolVersion, ID: 4, Op: broker.OpDescribePath, Path: "."})
	if resp.Error != nil || resp.Info == nil || resp.Info.Kind != string(core.PathKindDirectory) {
		t.Fatalf("describe after refusals = %+v", resp)
	}

	if _, err := clientConn.Write([]byte{0, 0, 0, 0}); err != nil {
		t.Fatal(err)
	}
	if err := <-served; !errors.Is(err, core.ErrInvalid) {
		t.Fatalf("ServeConnection after a malformed frame = %v, want ErrInvalid", err)
	}
}

// recordingSink keeps every published event for the test to inspect.
type recordingSink struct {
	mu     sync.Mutex
	events []core.Event
}

func (s *recordingSink) Publish(_ context.Context, event core.Event) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = append(s.events, event)
}

func (s *recordingSink) reportEventTypes() []core.EventType {
	s.mu.Lock()
	defer s.mu.Unlock()
	types := make([]core.EventType, 0, len(s.events))
	for _, event := range s.events {
		types = append(types, event.ReportEventType())
	}
	return types
}

// askingBroker asks before every OpenFile until it hears an approval, like a policy Ask.
type askingBroker struct {
	*broker.LocalBroker
	mu           sync.Mutex
	approvedOnce []core.Capability
	rules        []core.DecisionRule
}

func (b *askingBroker) OpenFile(ctx context.Context, path string) (core.OpenedFile, error) {
	b.mu.Lock()
	isApproved := len(b.approvedOnce)+len(b.rules) > 0
	b.mu.Unlock()
	if !isApproved {
		return core.OpenedFile{}, &core.ApprovalRequiredError{Operation: core.Operation{Kind: core.CapabilityFSRead, Target: path, Explanation: "ask: untrusted step"}}
	}
	return b.LocalBroker.OpenFile(ctx, path)
}

func (b *askingBroker) ApproveOnce(_ context.Context, approved core.Capability) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.approvedOnce = append(b.approvedOnce, approved)
	return nil
}

func (b *askingBroker) AddDecisionRule(_ context.Context, rule core.DecisionRule) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.rules = append(b.rules, rule)
	return nil
}

type scriptedApprover struct {
	decision core.ApprovalDecision
	asked    []core.Operation
}

func (a *scriptedApprover) RequestApproval(_ context.Context, op core.Operation) (core.ApprovalDecision, error) {
	a.asked = append(a.asked, op)
	return a.decision, nil
}

func TestRemoteBrokerPutsAnAskToTheHumanAndHonoursTheAnswer(t *testing.T) {
	for name, decision := range map[string]core.ApprovalDecision{"once": core.ApprovalOnce, "always": core.ApprovalAlways, "deny": core.ApprovalDenied} {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			writeWorkspaceFile(t, root, "a.txt", "a")
			asking := &askingBroker{LocalBroker: broker.BuildLocalBroker(root, core.SystemClock{})}
			approver, events := &scriptedApprover{decision: decision}, &recordingSink{}
			remote, _ := startPipedBrokerWith(t, asking, broker.RemoteOptions{Approver: approver, Events: events})

			opened, err := remote.OpenFile(context.Background(), "a.txt")
			if len(approver.asked) != 1 || approver.asked[0].Target != "a.txt" || approver.asked[0].Explanation != "ask: untrusted step" {
				t.Fatalf("asked %+v, want one question carrying the operation and its explanation", approver.asked)
			}
			switch decision {
			case core.ApprovalDenied:
				if !errors.Is(err, core.ErrDenied) || len(asking.approvedOnce)+len(asking.rules) != 0 {
					t.Fatalf("OpenFile after deny = %v, broker heard %+v %+v", err, asking.approvedOnce, asking.rules)
				}
				if types := events.reportEventTypes(); len(types) != 1 || types[0] != core.EventPolicyDenied {
					t.Fatalf("events = %v, want one PolicyDenied", types)
				}
				return
			case core.ApprovalOnce:
				if len(asking.approvedOnce) != 1 || asking.approvedOnce[0] != (core.Capability{Kind: core.CapabilityFSRead, Scope: "a.txt"}) {
					t.Fatalf("broker heard once-approvals %+v", asking.approvedOnce)
				}
			case core.ApprovalAlways:
				if len(asking.rules) != 1 || asking.rules[0].Scope != "a.txt" || asking.rules[0].Decision != core.DecisionAllow {
					t.Fatalf("broker heard rules %+v", asking.rules)
				}
			}
			if err != nil {
				t.Fatalf("OpenFile after approval = %v", err)
			}
			_ = opened.Content.Close()
		})
	}
}

func TestRemoteBrokerRefusesAnAskWithNobodyToAnswer(t *testing.T) {
	asking := &askingBroker{LocalBroker: broker.BuildLocalBroker(t.TempDir(), core.SystemClock{})}
	remote, _ := startPipedBroker(t, asking)
	if _, err := remote.OpenFile(context.Background(), "a.txt"); !errors.Is(err, core.ErrDenied) {
		t.Fatalf("OpenFile with no approver = %v, want ErrDenied", err)
	}
}

func TestRemoteBrokerWritesThroughTheEnforcedBroker(t *testing.T) {
	enforced, workspace, _ := buildSessionBroker(t, "default")
	events := &recordingSink{}
	remote, _ := startPipedBrokerWith(t, enforced, broker.RemoteOptions{Events: events})
	ctx := context.Background()

	info, err := remote.WriteFile(ctx, "notes/new.md", []byte("hello"))
	if err != nil || info.WorkspacePath != "notes/new.md" {
		t.Fatalf("WriteFile = %+v, %v", info, err)
	}
	if content, err := os.ReadFile(filepath.Join(workspace, "notes", "new.md")); err != nil || string(content) != "hello" {
		t.Fatalf("written content = %q, %v", content, err)
	}
	if _, err := remote.WriteFile(ctx, ".travis/decisions.toml", []byte("[[rule]]")); !errors.Is(err, core.ErrDenied) {
		t.Fatalf("WriteFile(.travis) = %v, want ErrDenied", err)
	}
	if types := events.reportEventTypes(); len(types) != 1 || types[0] != core.EventPolicyDenied {
		t.Fatalf("events = %v, want one PolicyDenied", types)
	}
	if _, err := remote.WriteFile(ctx, "big.bin", make([]byte, broker.MaxWriteFileBytes+1)); !errors.Is(err, core.ErrInvalid) {
		t.Fatalf("oversize WriteFile = %v, want ErrInvalid", err)
	}
	if _, err := remote.DescribePath(ctx, "notes/new.md"); err != nil {
		t.Fatalf("an oversize write broke the session: %v", err)
	}
}

func TestRemoteBrokerReportsTheBrokerUnavailableOnce(t *testing.T) {
	enforced, _, _ := buildSessionBroker(t, "paranoid")
	events := &recordingSink{}
	remote, stop := startPipedBrokerWith(t, enforced, broker.RemoteOptions{Events: events})
	if err := stop(); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		_, _ = remote.DescribePath(context.Background(), "a.txt")
	}
	if types := events.reportEventTypes(); len(types) != 1 || types[0] != core.EventBrokerUnavailable {
		t.Fatalf("events = %v, want exactly one BrokerUnavailable", types)
	}
}
