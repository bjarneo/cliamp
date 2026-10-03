package ipc

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
)

func shortTempDir(t *testing.T) string {
	t.Helper()
	// The temp dirs of the macOS and Windows runners make a socket path
	// longer than the Unix socket limit.
	base := ""
	switch runtime.GOOS {
	case "darwin":
		base = "/tmp"
	case "windows":
	default:
		return t.TempDir()
	}
	dir, err := os.MkdirTemp(base, "c")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
}

func TestV2EntryPointsReportErrNotRunning(t *testing.T) {
	sock := filepath.Join(shortTempDir(t), "missing.sock")
	if _, err := SendV2(sock, V2Request{Method: "state.get"}); !errors.Is(err, ErrNotRunning) {
		t.Errorf("SendV2 error = %v, want ErrNotRunning", err)
	}
	if _, err := SubscribeV2(sock, json.RawMessage(`"events"`), []string{"runtime.state"}); !errors.Is(err, ErrNotRunning) {
		t.Errorf("SubscribeV2 error = %v, want ErrNotRunning", err)
	}
	if err := StreamBands(context.Background(), sock, time.Millisecond, io.Discard); !errors.Is(err, ErrNotRunning) {
		t.Errorf("StreamBands error = %v, want ErrNotRunning", err)
	}
}

func TestNewServerSocketLifecycle(t *testing.T) {
	dir := shortTempDir(t)
	sock := filepath.Join(dir, "cliamp.sock")
	if err := os.WriteFile(sock, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	server, err := NewServer(sock)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(sock + ".pid"); err != nil {
		t.Fatalf("PID file: %v", err)
	}
	if err := server.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(sock); !os.IsNotExist(err) {
		t.Fatalf("socket exists after Close: %v", err)
	}
	if _, err := os.Stat(sock + ".pid"); !os.IsNotExist(err) {
		t.Fatalf("PID file exists after Close: %v", err)
	}
}

func TestNewServerRejectsLivePID(t *testing.T) {
	dir := shortTempDir(t)
	sock := filepath.Join(dir, "cliamp.sock")
	if err := os.WriteFile(sock+".pid", []byte(strconv.Itoa(os.Getpid())), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := NewServer(sock); err == nil || !strings.Contains(err.Error(), "already running") {
		t.Fatalf("NewServer() error = %v", err)
	}
}

func TestV2ServerRejectsUnversionedRequest(t *testing.T) {
	sock := filepath.Join(shortTempDir(t), "cliamp.sock")
	server, err := NewServer(sock)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = server.Close() })

	response := sendRawV2Request(t, sock, []byte(`{"cmd":"status"}`))
	if response.OK || errorCode(response.Error) != V2ErrorCodeInvalidVersion {
		t.Fatalf("response = %#v", response)
	}
}

func TestStreamBandsUsesV2SpectrumMethod(t *testing.T) {
	sock := filepath.Join(shortTempDir(t), "cliamp.sock")
	server, err := NewServer(sock)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = server.Close() })
	server.SetV2Dispatcher(V2DispatcherFunc(func(_ context.Context, request V2Request) (V2Result, *V2Error) {
		if request.Method != "spectrum.get" {
			t.Fatalf("method = %q", request.Method)
		}
		result, err := json.Marshal(Response{OK: true, Visualizer: "Bars", Bands: []float64{0.5}})
		if err != nil {
			t.Fatal(err)
		}
		return V2Result{Result: result}, nil
	}))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var output bytes.Buffer
	done := make(chan error, 1)
	go func() { done <- StreamBands(ctx, sock, time.Millisecond, &output) }()
	time.Sleep(20 * time.Millisecond)
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if got := output.String(); !strings.Contains(got, `"visualizer":"Bars"`) || !strings.Contains(got, `"bands":[0.5]`) {
		t.Fatalf("stream output = %q", got)
	}
}

// Listening finds a live server. A missing socket, a stale socket and a
// file that is not a socket are free. A path that no socket can have is an
// error.
func TestListening(t *testing.T) {
	for _, tt := range []struct {
		name    string
		setup   func(t *testing.T, sock string)
		sock    func(dir string) string
		want    bool
		wantErr bool
	}{
		{name: "no socket"},
		{name: "server", want: true, setup: func(t *testing.T, sock string) {
			server, err := NewServer(sock)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = server.Close() })
		}},
		{name: "stale socket", setup: func(t *testing.T, sock string) {
			ln, err := net.Listen("unix", sock)
			if err != nil {
				t.Fatal(err)
			}
			ln.(*net.UnixListener).SetUnlinkOnClose(false)
			_ = ln.Close()
		}},
		{name: "regular file", setup: func(t *testing.T, sock string) {
			if err := os.WriteFile(sock, nil, 0o600); err != nil {
				t.Fatal(err)
			}
		}},
		{
			name:    "path too long",
			sock:    func(dir string) string { return filepath.Join(dir, strings.Repeat("s", 120)) },
			wantErr: true,
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			dir := shortTempDir(t)
			sock := filepath.Join(dir, "cliamp.sock")
			if tt.sock != nil {
				sock = tt.sock(dir)
			}
			if tt.setup != nil {
				tt.setup(t, sock)
			}
			got, err := Listening(sock)
			if (err != nil) != tt.wantErr {
				t.Fatalf("Listening error = %v, want error %v", err, tt.wantErr)
			}
			if err != nil && !strings.HasPrefix(err.Error(), "ipc: probe socket ") {
				t.Fatalf("Listening error = %q, want the probe context", err)
			}
			if got != tt.want {
				t.Fatalf("Listening = %v, want %v", got, tt.want)
			}
		})
	}
}
