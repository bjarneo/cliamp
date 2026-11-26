//go:build !windows

package ipc

import (
	"errors"
	"fmt"
	"io"
	"os"
	"syscall"
)

const maxPIDFileSize = 64

// readPIDFileSafe opens a regular PID file without following symlinks and reads bounded bytes.
func readPIDFileSafe(pidPath string) ([]byte, error) {
	f, err := os.OpenFile(pidPath, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("ipc: pid file %s is not a regular file", pidPath)
	}
	return io.ReadAll(io.LimitReader(f, maxPIDFileSize))
}

// checkDirOwner rejects a socket directory owned by another user.
func checkDirOwner(info os.FileInfo) error {
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return nil
	}
	return peerUIDAllowed(st.Uid, os.Geteuid())
}

func processAlive(pid int) (bool, error) {
	if pid <= 0 {
		return false, nil
	}
	if pid == os.Getpid() {
		return true, nil
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false, fmt.Errorf("probe process liveness: %w", err)
	}
	err = proc.Signal(syscall.Signal(0))
	if err == nil {
		return true, nil
	}
	// os.Process.Signal converts the kernel's ESRCH into os.ErrProcessDone.
	if errors.Is(err, os.ErrProcessDone) || errors.Is(err, syscall.ESRCH) {
		return false, nil
	}
	// EPERM means the process exists but belongs to another user.
	if errors.Is(err, syscall.EPERM) {
		return true, nil
	}
	return false, fmt.Errorf("probe process liveness: %w", err)
}
