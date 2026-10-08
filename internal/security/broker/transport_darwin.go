package broker

import (
	"net"

	"golang.org/x/sys/unix"
)

// macOS splits the peer lookup: LOCAL_PEERPID for the process, LOCAL_PEERCRED for the user.
// Both describe the peer as of connect (§5.12.5).
func verifyPeerProcess(conn net.Conn, wantPID int) error {
	return readPeerSocketFD(conn, func(fd int) error {
		peerPID, err := unix.GetsockoptInt(fd, unix.SOL_LOCAL, unix.LOCAL_PEERPID)
		if err != nil {
			return err
		}
		peerCredentials, err := unix.GetsockoptXucred(fd, unix.SOL_LOCAL, unix.LOCAL_PEERCRED)
		if err != nil {
			return err
		}
		return requireExpectedPeer(peerPID, wantPID, peerCredentials.Uid)
	})
}
