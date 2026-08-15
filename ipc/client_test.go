package ipc

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
)

// shortTempDir returns a temp directory whose path is short enough for a Unix
// socket. macOS caps the socket path (sun_path) at 104 bytes, and t.TempDir()
// under /var/folders overflows that for longer test names; use a short /tmp base
// there. Other platforms keep t.TempDir().
func shortTempDir(t *testing.T) string {
	t.Helper()
	if runtime.GOOS != "darwin" {
		return t.TempDir()
	}
	d, err := os.MkdirTemp("/tmp", "c")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(d) })
	return d
}

// TestSendRoundTrip spins up a real server bound to a temp socket and exchanges
// one request/response through the client.
func TestSendRoundTrip(t *testing.T) {
	sock := filepath.Join(shortTempDir(t), "cliamp.sock")

	disp := &captureDispatcher{autoReply: Response{OK: true}}
	srv, err := NewServer(sock, disp)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	t.Cleanup(func() { _ = srv.Close() })

	resp, err := Send(sock, Request{Cmd: "play"})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if !resp.OK {
		t.Errorf("resp.OK = false, want true (err=%q)", resp.Error)
	}
	if _, ok := disp.last.(PlayMsg); !ok {
		t.Errorf("server received %T, want PlayMsg", disp.last)
	}
}

func TestSendNoServer(t *testing.T) {
	sock := filepath.Join(shortTempDir(t), "missing.sock")

	_, err := Send(sock, Request{Cmd: "status"})
	if err == nil {
		t.Fatal("Send to missing socket should error")
	}
	if !errors.Is(err, ErrNotRunning) {
		t.Errorf("error = %v, want ErrNotRunning", err)
	}
	if !strings.Contains(err.Error(), sock) {
		t.Errorf("error = %q, want the socket path", err.Error())
	}
}

// Every entry point reports a missing server through the same sentinel so
// callers can branch without matching message text.
func TestEntryPointsReportErrNotRunning(t *testing.T) {
	sock := filepath.Join(shortTempDir(t), "missing.sock")

	if _, err := Send(sock, Request{Cmd: "status"}); !errors.Is(err, ErrNotRunning) {
		t.Errorf("Send error = %v, want ErrNotRunning", err)
	}
	if _, err := Subscribe(sock, []string{"plugin.test.playback"}); !errors.Is(err, ErrNotRunning) {
		t.Errorf("Subscribe error = %v, want ErrNotRunning", err)
	}
	if err := StreamBands(context.Background(), sock, time.Millisecond, io.Discard); !errors.Is(err, ErrNotRunning) {
		t.Errorf("StreamBands error = %v, want ErrNotRunning", err)
	}
}

func TestSendInvalidRequestReturnsError(t *testing.T) {
	// Server responds to an unknown cmd with OK:false, Error:"unknown command:...".
	sock := filepath.Join(shortTempDir(t), "cliamp.sock")
	srv, err := NewServer(sock, &captureDispatcher{})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	t.Cleanup(func() { _ = srv.Close() })

	resp, err := Send(sock, Request{Cmd: "doesnotexist"})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if resp.OK {
		t.Error("unknown cmd should return !OK")
	}
	if !strings.Contains(resp.Error, "unknown command") {
		t.Errorf("error = %q, want to mention 'unknown command'", resp.Error)
	}
}

func TestDefaultSocketPath(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	p := DefaultSocketPath()
	if !strings.HasSuffix(p, filepath.Join("cliamp", "cliamp.sock")) {
		t.Errorf("DefaultSocketPath = %q, want to end with cliamp/cliamp.sock", p)
	}
}

func TestNewServerRemovesOrphanSocket(t *testing.T) {
	dir := shortTempDir(t)
	sock := filepath.Join(dir, "cliamp.sock")

	// Create an orphan socket file with no PID file — NewServer should remove it.
	if err := os.WriteFile(sock, []byte(""), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	srv, err := NewServer(sock, &captureDispatcher{})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	t.Cleanup(func() { _ = srv.Close() })

	// Server is up and serving.
	resp, err := Send(sock, Request{Cmd: "play"})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if !resp.OK {
		t.Errorf("resp.OK = false, want true")
	}
}

func TestNewServerCorruptPIDFile(t *testing.T) {
	dir := shortTempDir(t)
	sock := filepath.Join(dir, "cliamp.sock")

	// Corrupt PID file is cleaned and NewServer succeeds.
	if err := os.WriteFile(sock+".pid", []byte("notanumber"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	srv, err := NewServer(sock, &captureDispatcher{})
	if err != nil {
		t.Fatalf("NewServer with corrupt PID: %v", err)
	}
	_ = srv.Close()
}

func TestNewServerDeadPIDFile(t *testing.T) {
	dir := shortTempDir(t)
	sock := filepath.Join(dir, "cliamp.sock")

	// PID 1 is init (alive), but a far-out-of-range PID should be dead on Linux.
	// Pick a PID unlikely to exist (>2^30 PIDs don't normally exist on Linux).
	deadPID := 0x3FFFFFFF
	if err := os.WriteFile(sock+".pid", []byte(strconv.Itoa(deadPID)), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	srv, err := NewServer(sock, &captureDispatcher{})
	if err != nil {
		t.Fatalf("NewServer with dead PID: %v", err)
	}
	_ = srv.Close()
}

func TestNewServerLivePIDReturnsError(t *testing.T) {
	dir := shortTempDir(t)
	sock := filepath.Join(dir, "cliamp.sock")

	// Our own PID is definitely live → NewServer should refuse to start.
	if err := os.WriteFile(sock+".pid", []byte(strconv.Itoa(os.Getpid())), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	srv, err := NewServer(sock, &captureDispatcher{})
	if err == nil {
		_ = srv.Close()
		t.Fatal("NewServer should error when PID file contains a live process")
	}
	if !strings.Contains(err.Error(), "already running") {
		t.Errorf("error = %q, want to mention 'already running'", err.Error())
	}
}

func TestServerCloseRemovesFiles(t *testing.T) {
	sock := filepath.Join(shortTempDir(t), "cliamp.sock")
	srv, err := NewServer(sock, &captureDispatcher{})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}

	if _, err := os.Stat(sock); err != nil {
		t.Fatalf("socket should exist after NewServer: %v", err)
	}
	if _, err := os.Stat(sock + ".pid"); err != nil {
		t.Fatalf("pid file should exist after NewServer: %v", err)
	}

	if err := srv.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	if _, err := os.Stat(sock); !os.IsNotExist(err) {
		t.Errorf("socket still exists after Close: %v", err)
	}
	if _, err := os.Stat(sock + ".pid"); !os.IsNotExist(err) {
		t.Errorf("pid file still exists after Close: %v", err)
	}
}

func TestServerMultipleRequestsSameConnection(t *testing.T) {
	// Make sure the server can handle multiple requests over a single socket.
	// Each Send opens its own connection, so this really verifies the accept
	// loop keeps going beyond the first request.
	sock := filepath.Join(shortTempDir(t), "cliamp.sock")
	disp := &captureDispatcher{}
	srv, err := NewServer(sock, disp)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	t.Cleanup(func() { _ = srv.Close() })

	for _, cmd := range []string{"play", "pause", "next", "prev"} {
		resp, err := Send(sock, Request{Cmd: cmd})
		if err != nil {
			t.Fatalf("Send %s: %v", cmd, err)
		}
		if !resp.OK {
			t.Errorf("cmd %s OK=false, err=%q", cmd, resp.Error)
		}
	}
}

func TestServerHandlesInvalidJSON(t *testing.T) {
	sock := filepath.Join(shortTempDir(t), "cliamp.sock")
	srv, err := NewServer(sock, &captureDispatcher{})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	t.Cleanup(func() { _ = srv.Close() })

	// Connect raw, send garbage, read response line.
	conn, err := dialWithTimeout(sock, time.Second)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	if _, err := conn.Write([]byte("not valid json\n")); err != nil {
		t.Fatalf("write: %v", err)
	}
	buf := make([]byte, 512)
	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	n, err := conn.Read(buf)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !strings.Contains(string(buf[:n]), "invalid JSON") {
		t.Errorf("response = %q, want to mention 'invalid JSON'", string(buf[:n]))
	}
}
