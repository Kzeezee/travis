package broker

import (
	"context"
	"net"
)

// Windows transport: a named pipe restricted to the current user, peer checked with
// GetNamedPipeClientProcessId (§5.12.5). Stubbed until the Windows owner fills it in (D-25).

func buildEndpointAddress(_, nonce string) (string, error) {
	return `\\.\pipe\travis-` + nonce, nil
}

func listenOnEndpoint(SessionEndpoint) (net.Listener, error) {
	return nil, errTransportNotBuilt
}

func dialEndpoint(context.Context, SessionEndpoint) (net.Conn, error) {
	return nil, errTransportNotBuilt
}

func verifyPeerProcess(net.Conn, int) error {
	return errTransportNotBuilt
}
