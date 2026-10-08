package broker

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net"

	"github.com/Kzeezee/travis/internal/core"
)

// The per-OS transport files (transport_<os>.go) each provide the same four functions:
//
//	buildEndpointAddress(socketDir, nonce string) (string, error)
//	listenOnEndpoint(endpoint SessionEndpoint) (net.Listener, error)
//	dialEndpoint(ctx context.Context, endpoint SessionEndpoint) (net.Conn, error)
//	verifyPeerProcess(conn net.Conn, wantPID int) error
//
// Everything above them (framing, policy, the client) is OS-independent (§5.12.5).

const endpointNonceBytes = 16

// errTransportNotBuilt is returned by an OS whose transport is still a stub (D-25).
var errTransportNotBuilt = fmt.Errorf("the broker transport for this OS is not built yet: %w", core.ErrUnsupported)

// SessionEndpoint is where one session's broker listens: a Unix socket path or a Windows pipe
// name, carrying a random nonce so another local process cannot guess it (D-12).
type SessionEndpoint struct {
	Address string
}

// BuildSessionEndpoint names a fresh endpoint for one session. socketDir is where a Unix
// socket is created; Windows pipes live in their own namespace and ignore it.
func BuildSessionEndpoint(socketDir string) (SessionEndpoint, error) {
	nonce := make([]byte, endpointNonceBytes)
	if _, err := rand.Read(nonce); err != nil {
		return SessionEndpoint{}, fmt.Errorf("broker endpoint nonce: %w", err)
	}
	address, err := buildEndpointAddress(socketDir, hex.EncodeToString(nonce))
	if err != nil {
		return SessionEndpoint{}, err
	}
	return SessionEndpoint{Address: address}, nil
}

// ServeSession is the broker process's main loop: it accepts exactly one connection on
// endpoint, refuses it unless it comes from clientPID, and serves it until it closes.
func ServeSession(ctx context.Context, endpoint SessionEndpoint, clientPID int, effects core.Broker) error {
	listener, err := listenOnEndpoint(endpoint)
	if err != nil {
		return fmt.Errorf("broker listen on %q: %w", endpoint.Address, err)
	}
	stopClosingOnCancel := context.AfterFunc(ctx, func() { _ = listener.Close() })
	defer stopClosingOnCancel()

	conn, err := listener.Accept()
	// One client per session (D-12): nobody else gets to connect after the first.
	closeErr := listener.Close()
	if err != nil {
		return reportServeEnd(ctx, err)
	}
	if closeErr != nil && !errors.Is(closeErr, net.ErrClosed) {
		_ = conn.Close()
		return fmt.Errorf("broker stop listening: %w", closeErr)
	}
	if err := verifyPeerProcess(conn, clientPID); err != nil {
		_ = conn.Close()
		return fmt.Errorf("broker refused its client: %w", err)
	}
	defer conn.Close()
	return ServeConnection(ctx, conn, effects)
}

// ConnectRemoteBroker dials the session's broker and refuses it unless it is brokerPID, so a
// process squatting on the endpoint cannot pose as the broker.
func ConnectRemoteBroker(ctx context.Context, endpoint SessionEndpoint, brokerPID int) (*RemoteBroker, error) {
	conn, err := dialEndpoint(ctx, endpoint)
	if err != nil {
		return nil, fmt.Errorf("broker unreachable at %q: %w", endpoint.Address, err)
	}
	if err := verifyPeerProcess(conn, brokerPID); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("refused broker at %q: %w", endpoint.Address, err)
	}
	return BuildRemoteBroker(conn), nil
}
