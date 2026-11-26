//go:build !linux && !darwin

package ipc

import "net"

// verifyPeerCred is a no-op on platforms without Unix peer credentials.
func verifyPeerCred(conn net.Conn) error {
	return nil
}
