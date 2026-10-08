package broker

import (
	"net"

	"golang.org/x/sys/unix"
)

// SO_PEERCRED reports the peer's credentials as of connect, so handing the socket to another
// process afterwards does not change who it identifies (§5.12.5).
func verifyPeerProcess(conn net.Conn, wantPID int) error {
	return readPeerSocketFD(conn, func(fd int) error {
		peerCredentials, err := unix.GetsockoptUcred(fd, unix.SOL_SOCKET, unix.SO_PEERCRED)
		if err != nil {
			return err
		}
		return requireExpectedPeer(int(peerCredentials.Pid), wantPID, peerCredentials.Uid)
	})
}
