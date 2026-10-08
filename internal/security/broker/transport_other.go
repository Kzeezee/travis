//go:build !linux && !darwin && !windows

package broker

import (
	"context"
	"fmt"
	"net"

	"github.com/Kzeezee/travis/internal/core"
)

// Any other OS compiles but has no broker transport, so every tool fails closed (§8.1).

var errTransportUnsupportedOS = fmt.Errorf("no broker transport exists for this OS: %w", core.ErrUnsupported)

func createEndpointForOS(string, string) (SessionEndpoint, func(), error) {
	return SessionEndpoint{}, nil, errTransportUnsupportedOS
}

func listenOnEndpoint(SessionEndpoint) (net.Listener, error) {
	return nil, errTransportUnsupportedOS
}

func dialEndpoint(context.Context, SessionEndpoint) (net.Conn, error) {
	return nil, errTransportUnsupportedOS
}

func verifyPeerProcess(net.Conn, int) error {
	return errTransportUnsupportedOS
}
