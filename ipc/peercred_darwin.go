//go:build darwin

package ipc

import (
	"fmt"
	"net"
	"os"

	"golang.org/x/sys/unix"
)

// verifyPeerCred checks that the connected Unix socket peer matches this process user.
func verifyPeerCred(conn net.Conn) error {
	unixConn, ok := conn.(*net.UnixConn)
	if !ok {
		return fmt.Errorf("ipc: unexpected connection type %T", conn)
	}
	rawConn, err := unixConn.SyscallConn()
	if err != nil {
		return fmt.Errorf("ipc: syscall conn: %w", err)
	}
	var cred *unix.Xucred
	var sockErr error
	if err := rawConn.Control(func(fd uintptr) {
		cred, sockErr = unix.GetsockoptXucred(int(fd), unix.SOL_LOCAL, unix.LOCAL_PEERCRED)
	}); err != nil {
		return fmt.Errorf("ipc: control socket: %w", err)
	}
	if sockErr != nil {
		return fmt.Errorf("ipc: get peer credentials: %w", sockErr)
	}
	return peerUIDAllowed(cred.Uid, os.Geteuid())
}
