package broker

import (
	"context"
	"fmt"
	"net"
	"os"

	"github.com/Microsoft/go-winio"
	"golang.org/x/sys/windows"

	"github.com/Kzeezee/travis/internal/core"
)

// Windows transport: a named pipe only the current user can open, peer checked with
// GetNamedPipeClientProcessId / GetNamedPipeServerProcessId (§5.12.5). Pipes have their own
// namespace, so there is no directory to create or remove and runtimeDir goes unused.

func createEndpointForOS(_, nonce string) (SessionEndpoint, func(), error) {
	return SessionEndpoint(`\\.\pipe\travis-` + nonce), func() {}, nil
}

// listenOnEndpoint creates the pipe's first instance, so a process that created the name
// first makes this fail rather than serve as the broker; the protected DACL admits only us.
func listenOnEndpoint(endpoint SessionEndpoint) (net.Listener, error) {
	tokenUser, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return nil, fmt.Errorf("broker pipe owner: %w", err)
	}
	onlyThisUser := "D:P(A;;GA;;;" + tokenUser.User.Sid.String() + ")"
	return winio.ListenPipe(string(endpoint), &winio.PipeConfig{SecurityDescriptor: onlyThisUser})
}

func dialEndpoint(ctx context.Context, endpoint SessionEndpoint) (net.Conn, error) {
	return winio.DialPipeContext(ctx, string(endpoint))
}

// verifyPeerProcess works from either end: the peer is whichever of the pipe's client and
// server is not this process. The DACL already restricts both ends to this user.
func verifyPeerProcess(conn net.Conn, wantPID int) error {
	pipe, isPipe := conn.(interface{ Fd() uintptr })
	if !isPipe {
		return fmt.Errorf("peer check needs a named pipe, got %T: %w", conn, core.ErrDenied)
	}
	handle := windows.Handle(pipe.Fd())
	var clientPID, serverPID uint32
	if err := windows.GetNamedPipeClientProcessId(handle, &clientPID); err != nil {
		return fmt.Errorf("pipe client pid: %w", err)
	}
	if err := windows.GetNamedPipeServerProcessId(handle, &serverPID); err != nil {
		return fmt.Errorf("pipe server pid: %w", err)
	}
	peerPID := clientPID
	if int(clientPID) == os.Getpid() {
		peerPID = serverPID
	}
	if int(peerPID) != wantPID {
		return fmt.Errorf("peer is pid %d, want pid %d: %w", peerPID, wantPID, core.ErrDenied)
	}
	return nil
}
