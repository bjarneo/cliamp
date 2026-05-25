package ipc

import (
	"net"
	"time"
)

// dialWithTimeout exposes a raw Unix-socket dial helper so tests in this
// package can send arbitrary bytes instead of going through Send (which
// wraps a Request as JSON).
func dialWithTimeout(sockPath string, d time.Duration) (net.Conn, error) {
	return dialSocket(sockPath, d)
}
