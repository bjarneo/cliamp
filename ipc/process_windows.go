//go:build windows

package ipc

import (
	"context"
	"encoding/csv"
	"fmt"
	"os"
	"os/exec"
	"strconv"
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
	r := csv.NewReader(strings.NewReader(string(out)))
	r.FieldsPerRecord = -1
	records, err := r.ReadAll()
	if err != nil {
		return false, fmt.Errorf("parse tasklist output: %w", err)
	}
	pidStr := strconv.Itoa(pid)
	for _, record := range records {
		if len(record) > 1 {
			if strings.Trim(record[1], ` "`) == pidStr {
				return true, nil
			}
		}
	}
	return false, nil
}
