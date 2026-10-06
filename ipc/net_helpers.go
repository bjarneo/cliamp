package ipc

import (
	"errors"
	"fmt"
	"net"
	"os"
	"strings"
	"syscall"
	"time"
)

// ErrNotRunning reports that nothing is listening on the IPC socket. It carries
// no CLI wording on purpose: command-layer callers match it with errors.Is and
// render the user-facing message themselves.
var ErrNotRunning = errors.New("no listener on socket")

// maxSocketPathLen is the longest socket path that the platform accepts.
// The sun_path field of the address also holds the NUL that ends the path.
const maxSocketPathLen = len(syscall.RawSockaddrUnix{}.Path) - 1

// errSocketPathTooLong reports a socket path longer than maxSocketPathLen.
// The kernel reports such a path only as an invalid argument.
var errSocketPathTooLong = errors.New("socket path is too long")

func checkSocketPath(sockPath string) error {
	if len(sockPath) <= maxSocketPathLen {
		return nil
	}
	return fmt.Errorf("%w: %d bytes, the limit is %d. Set CLIAMP_CONFIG_DIR or XDG_CONFIG_HOME to a shorter directory",
		errSocketPathTooLong, len(sockPath), maxSocketPathLen)
}

func dialSocket(sockPath string, timeout time.Duration) (net.Conn, error) {
	if err := checkSocketPath(sockPath); err != nil {
		return nil, err
	}
	return net.DialTimeout("unix", sockPath, timeout)
}

func listenSocket(sockPath string) (net.Listener, error) {
	if err := checkSocketPath(sockPath); err != nil {
		return nil, err
	}
	return net.Listen("unix", sockPath)
}

// wsaeConnRefused is Windows' WSAECONNREFUSED, returned when dialing an
// AF_UNIX socket nobody is listening on.
const wsaeConnRefused = syscall.Errno(10061)

func isSocketUnavailable(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, os.ErrNotExist) || errors.Is(err, syscall.ECONNREFUSED) || errors.Is(err, syscall.ENOTSOCK) || errors.Is(err, wsaeConnRefused) {
		return true
	}
	// Last resort for platform errors that arrive untyped (Windows AF_UNIX
	// messages vary by version).
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "refused") ||
		strings.Contains(msg, "non-socket") ||
		strings.Contains(msg, "dead network") ||
		strings.Contains(msg, "no such file") ||
		strings.Contains(msg, "cannot find the file")
}
