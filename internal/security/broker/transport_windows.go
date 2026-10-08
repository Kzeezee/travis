package broker

import (
	"context"
	"fmt"
	"net"

	"github.com/Kzeezee/travis/internal/core"
)

// Windows transport: a named pipe restricted to the current user, peer checked with
// GetNamedPipeClientProcessId / GetNamedPipeServerProcessId (§5.12.5). Stubbed until the
// Windows owner fills it in (D-25); until then travis refuses to start a chat here. The
// endpoint will be `\\.\pipe\travis-<nonce>`, with no directory to create or remove.

var errTransportNotBuilt = fmt.Errorf("the broker transport for Windows is not built yet: %w", core.ErrUnsupported)

func createEndpointForOS(string, string) (SessionEndpoint, func(), error) {
	return SessionEndpoint{}, nil, errTransportNotBuilt
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
