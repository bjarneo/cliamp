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

func isSocketUnavailable(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, os.ErrNotExist) || errors.Is(err, syscall.ECONNREFUSED) {
		return true
	}
	var errno syscall.Errno
	if errors.As(err, &errno) {
		if errno == syscall.ECONNREFUSED || errno == syscall.Errno(10061) {
			return true
		}
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "refused") ||
		strings.Contains(msg, "dead network") ||
		strings.Contains(msg, "no such file") ||
		strings.Contains(msg, "cannot find the file")
}