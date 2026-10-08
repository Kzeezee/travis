//go:build linux || darwin

package broker

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"

	"github.com/Kzeezee/travis/internal/core"
)

// Unix transport shared by Linux and macOS: a socket in a private per-session directory.
// Only the peer credential lookup differs, in transport_linux.go and transport_darwin.go.

// maxUnixSocketPathBytes is macOS's sun_path size; Linux allows 108, so this is the stricter bound.
const maxUnixSocketPathBytes = 104

func createEndpointForOS(runtimeDir, nonce string) (SessionEndpoint, func(), error) {
	if err := os.MkdirAll(runtimeDir, 0o700); err != nil {
		return SessionEndpoint{}, nil, fmt.Errorf("broker runtime directory: %w", err)
	}
	// Mkdir, not MkdirAll: a directory someone else created first must not be adopted.
	sessionDir := filepath.Join(runtimeDir, "travis-"+nonce)
	if err := os.Mkdir(sessionDir, 0o700); err != nil {
		return SessionEndpoint{}, nil, fmt.Errorf("broker session directory: %w", err)
	}
	removeEndpoint := func() { _ = os.RemoveAll(sessionDir) }

	socketPath := filepath.Join(sessionDir, "b.sock")
	if len(socketPath) >= maxUnixSocketPathBytes {
		removeEndpoint()
		return SessionEndpoint{}, nil, fmt.Errorf("broker socket path %q is %d bytes, over the OS limit of %d: %w",
			socketPath, len(socketPath), maxUnixSocketPathBytes-1, core.ErrInvalid)
	}
	return SessionEndpoint{Address: socketPath}, removeEndpoint, nil
}

func listenOnEndpoint(endpoint SessionEndpoint) (net.Listener, error) {
	socketListener, err := net.ListenUnix("unix", &net.UnixAddr{Name: endpoint.Address, Net: "unix"})
	if err != nil {
		return nil, err
	}
	socketListener.SetUnlinkOnClose(true)
	return socketListener, nil
}

func dialEndpoint(ctx context.Context, endpoint SessionEndpoint) (net.Conn, error) {
	var dialer net.Dialer
	return dialer.DialContext(ctx, "unix", endpoint.Address)
}

// readPeerSocketFD runs readPeer on conn's file descriptor; only Unix socket connections qualify.
func readPeerSocketFD(conn net.Conn, readPeer func(fd int) error) error {
	unixConn, isUnixConn := conn.(*net.UnixConn)
	if !isUnixConn {
		return fmt.Errorf("peer check needs a unix socket, got %T: %w", conn, core.ErrDenied)
	}
	rawConn, err := unixConn.SyscallConn()
	if err != nil {
		return err
	}
	var peerErr error
	if err := rawConn.Control(func(fd uintptr) { peerErr = readPeer(int(fd)) }); err != nil {
		return err
	}
	return peerErr
}

// requireExpectedPeer accepts only wantPID running as this same user.
func requireExpectedPeer(peerPID, wantPID int, peerUID uint32) error {
	if peerPID != wantPID || int64(peerUID) != int64(os.Getuid()) {
		return fmt.Errorf("peer is pid %d uid %d, want pid %d uid %d: %w", peerPID, peerUID, wantPID, os.Getuid(), core.ErrDenied)
	}
	return nil
}
