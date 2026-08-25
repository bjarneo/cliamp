//go:build linux

package player

import (
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/bjarneo/cliamp/applog"
)

type pwReservation struct {
	cmd       *exec.Cmd
	done      chan error
	closeOnce sync.Once
	closeErr  error
}

func acquireAudioReservation(name string) (reservationHandle, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, nil
	}
	path, err := exec.LookPath("pw-reserve")
	if err != nil {
		return nil, errors.New("audio reservation requested, but 'pw-reserve' was not found in PATH")
	}
	stderr := &lockedBuffer{}
	cmd := exec.Command(path, "-n", name, "-a", "cliamp", "-r")
	cmd.Stdout = stderr
	cmd.Stderr = stderr
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start pw-reserve for %s: %w", name, err)
	}
	r := &pwReservation{cmd: cmd, done: make(chan error, 1)}
	go func() { r.done <- cmd.Wait() }()

	// ponytail: pw-reserve has no machine-readable ready signal; keep the 300 ms
	// liveness check until direct ReserveDevice1 D-Bus support is justified.
	select {
	case err := <-r.done:
		detail := strings.TrimSpace(stderr.String())
		if err == nil {
			err = errors.New("pw-reserve exited immediately")
		}
		if detail != "" {
			return nil, fmt.Errorf("reserve audio device %s: %w: %s", name, err, detail)
		}
		return nil, fmt.Errorf("reserve audio device %s: %w", name, err)
	case <-time.After(300 * time.Millisecond):
	}
	applog.Info("audio reservation acquired: %s", name)
	return r, nil
}

func (r *pwReservation) Close() error {
	r.closeOnce.Do(func() {
		if r.cmd == nil || r.cmd.Process == nil {
			return
		}
		if err := r.cmd.Process.Signal(syscall.SIGTERM); err != nil {
			select {
			case <-r.done:
				return
			default:
				r.closeErr = err
				return
			}
		}
		select {
		case err := <-r.done:
			if err != nil {
				r.closeErr = fmt.Errorf("pw-reserve exit: %w", err)
			}
		case <-time.After(2 * time.Second):
			_ = r.cmd.Process.Kill()
			<-r.done
			r.closeErr = errors.New("pw-reserve did not exit after SIGTERM")
		}
		applog.Info("audio reservation released")
	})
	return r.closeErr
}
