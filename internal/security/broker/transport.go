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

// Per-OS endpoint, listen, dial and peer-check functions live in transport_*.go (§5.12.5).

// endpointNonceBytes keeps names unguessable while leaving room under macOS's socket path limit.
const endpointNonceBytes = 8

// SessionEndpoint is where one session's broker listens: a Unix socket path or a Windows pipe
// name, carrying a random nonce so another local process cannot guess it (D-12).
type SessionEndpoint string

// CreateSessionEndpoint makes a fresh endpoint for one session under runtimeDir. On Unix it
// creates a private directory for the socket; removeEndpoint deletes whatever was created.
func CreateSessionEndpoint(runtimeDir string) (endpoint SessionEndpoint, removeEndpoint func(), err error) {
	nonce := make([]byte, endpointNonceBytes)
	if _, err := rand.Read(nonce); err != nil {
		return "", nil, fmt.Errorf("broker endpoint nonce: %w", err)
	}
	return createEndpointForOS(runtimeDir, hex.EncodeToString(nonce))
}

// SessionListener is a broker endpoint that is open but has not accepted its client yet.
type SessionListener struct {
	listener net.Listener
}

func ListenForSession(endpoint SessionEndpoint) (*SessionListener, error) {
	listener, err := listenOnEndpoint(endpoint)
	if err != nil {
		return nil, fmt.Errorf("broker listen on %q: %w", endpoint, err)
	}
	return &SessionListener{listener: listener}, nil
}

// ServeOneClient accepts exactly one connection, refuses it unless it comes from clientPID,
// and serves it until it closes. The listener is closed either way.
func (s *SessionListener) ServeOneClient(ctx context.Context, clientPID int, effects core.Broker) error {
	stopClosingOnCancel := context.AfterFunc(ctx, func() { _ = s.listener.Close() })
	defer stopClosingOnCancel()

	conn, err := s.listener.Accept()
	// One client per session (D-12): nobody else gets to connect after the first.
	closeErr := s.listener.Close()
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

// Close stops listening without serving, for a broker that fails before its client arrives.
func (s *SessionListener) Close() error {
	return s.listener.Close()
}

// ConnectRemoteBroker dials the session's broker and refuses it unless it is brokerPID, so a
// process squatting on the endpoint cannot pose as the broker.
func ConnectRemoteBroker(ctx context.Context, endpoint SessionEndpoint, brokerPID int, options RemoteOptions) (*RemoteBroker, error) {
	conn, err := dialEndpoint(ctx, endpoint)
	if err != nil {
		return nil, fmt.Errorf("broker unreachable at %q: %w", endpoint, err)
	}
	if err := verifyPeerProcess(conn, brokerPID); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("refused broker at %q: %w", endpoint, err)
	}
	return BuildRemoteBroker(conn, options), nil
}
