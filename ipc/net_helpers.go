package ipc

import (
	"errors"
	"net"
	"os"
	"strings"
	"syscall"
	"time"
)

func dialSocket(sockPath string, timeout time.Duration) (net.Conn, error) {
	return net.DialTimeout("unix", sockPath, timeout)
}

func listenSocket(sockPath string) (net.Listener, error) {
	return net.Listen("unix", sockPath)
}

// wsaeConnRefused is Windows' WSAECONNREFUSED, returned when dialing an
// AF_UNIX socket nobody is listening on.
const wsaeConnRefused = syscall.Errno(10061)

func isSocketUnavailable(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, os.ErrNotExist) || errors.Is(err, syscall.ECONNREFUSED) || errors.Is(err, wsaeConnRefused) {
		return true
	}
	// Last resort for platform errors that arrive untyped (Windows AF_UNIX
	// messages vary by version).
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "refused") ||
		strings.Contains(msg, "dead network") ||
		strings.Contains(msg, "no such file") ||
		strings.Contains(msg, "cannot find the file")
}
