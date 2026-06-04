//go:build !windows

package ipc

import (
	"errors"
	"fmt"
	"os"
	"syscall"
)

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
