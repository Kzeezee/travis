package broker

import (
	"context"
	"net"
	"path/filepath"
)

// Linux transport: a Unix domain socket, peer checked with SO_PEERCRED (§5.12.5). Stubbed until
// the Linux owner fills it in (D-25).

func buildEndpointAddress(socketDir, nonce string) (string, error) {
	return filepath.Join(socketDir, "travis-"+nonce+".sock"), nil
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
