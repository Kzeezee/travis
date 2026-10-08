package broker

import (
	"context"
	"net"
	"path/filepath"
)

// macOS transport: a Unix domain socket, peer checked with LOCAL_PEERCRED and LOCAL_PEERPID
// (§5.12.5). Keep the socket path under 104 bytes. Stubbed until the macOS owner fills it in (D-25).

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
