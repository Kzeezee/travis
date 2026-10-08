package broker_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
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
	serverConn, clientConn := net.Pipe()
	ctx, cancel := context.WithCancel(context.Background())
	served := make(chan error, 1)
	go func() { served <- broker.ServeConnection(ctx, serverConn, effects) }()

	remote := broker.BuildRemoteBroker(clientConn)
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
	effects := broker.BuildLocalBroker(t.TempDir())
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
