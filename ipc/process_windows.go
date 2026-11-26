//go:build windows

package ipc

import (
	"context"
	"encoding/csv"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

const maxPIDFileSize = 64

// readPIDFileSafe opens a regular PID file without following symlinks and reads bounded bytes.
func readPIDFileSafe(pidPath string) ([]byte, error) {
	linfo, err := os.Lstat(pidPath)
	if err != nil {
		return nil, err
	}
	if linfo.Mode()&os.ModeSymlink != 0 || !linfo.Mode().IsRegular() {
		return nil, fmt.Errorf("ipc: pid file %s is not a regular file", pidPath)
	}
	f, err := os.Open(pidPath)
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

// checkDirOwner is a no-op on Windows.
func checkDirOwner(os.FileInfo) error {
	return nil
}

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
