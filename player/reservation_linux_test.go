//go:build linux

package player

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestAudioReservationMissingHelper(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	if _, err := acquireAudioReservation("Audio2"); err == nil || !strings.Contains(err.Error(), "pw-reserve") {
		t.Fatalf("acquireAudioReservation() = %v", err)
	}
}

func TestAudioReservationSuccessfulEarlyExit(t *testing.T) {
	dir := t.TempDir()
	helper := filepath.Join(dir, "pw-reserve")
	if err := os.WriteFile(helper, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	_, err := acquireAudioReservation("Audio2")
	if err == nil || !strings.Contains(err.Error(), "pw-reserve exited immediately") {
		t.Fatalf("acquireAudioReservation() = %v", err)
	}
}

func TestPWReservationCloseStopsHelper(t *testing.T) {
	cmd := exec.Command("/bin/sh", "-c", "trap 'exit 0' TERM; while :; do /bin/sleep 0.05; done")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	r := &pwReservation{cmd: cmd, done: make(chan error, 1)}
	go func() { r.done <- cmd.Wait() }()
	time.Sleep(50 * time.Millisecond)
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	if cmd.ProcessState == nil || !cmd.ProcessState.Exited() {
		t.Fatal("reservation helper still running after Close")
	}
	if err := r.Close(); err != nil {
		t.Fatalf("second Close() = %v", err)
	}
}
