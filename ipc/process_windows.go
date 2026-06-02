//go:build windows

package ipc

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

func processAlive(pid int) (bool, error) {
	if pid <= 0 {
		return false, nil
	}
	if pid == os.Getpid() {
		return true, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "tasklist", "/FI", fmt.Sprintf("PID eq %d", pid), "/FO", "CSV", "/NH").Output()
	if err != nil {
		return false, fmt.Errorf("probe process liveness: %w", err)
	}
	return strings.Contains(string(out), fmt.Sprintf(`,"%d",`, pid)), nil
}
