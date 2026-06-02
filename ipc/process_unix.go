//go:build !windows

package ipc

import (
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
	if err == syscall.ESRCH {
		return false, nil
	}
	if err == syscall.EPERM {
		return true, nil
	}
	return false, fmt.Errorf("probe process liveness: %w", err)
}