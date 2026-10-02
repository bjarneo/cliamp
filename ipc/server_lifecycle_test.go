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
	"sync"
	"testing"
	"time"

	"github.com/bjarneo/cliamp/internal/fileutil"
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

// leaveStaleSocket leaves a socket inode at sock with no listener behind, the
// way a killed daemon does.
func leaveStaleSocket(t *testing.T, sock string) {
	t.Helper()
	stale, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	stale.(*net.UnixListener).SetUnlinkOnClose(false)
	if err := stale.Close(); err != nil {
		t.Fatal(err)
	}
}

// A live server holding the socket must never be displaced. This is the
// protection that actually matters, and it comes from the failed bind.
func TestNewServerRejectsLiveServer(t *testing.T) {
	sock := filepath.Join(shortTempDir(t), "cliamp.sock")
	server, err := NewServer(sock)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = server.Close() })

	if _, err := NewServer(sock); err == nil || !strings.Contains(err.Error(), "already running") {
		t.Fatalf("NewServer() against a live server = %v, want already running", err)
	}
}

// A PID file left behind by an unclean exit can name a live process that is not
// cliamp, because PIDs are reused. That must not stop the daemon from binding,
// or it never recovers without manual cleanup. See #591.
func TestNewServerIgnoresStalePIDOfUnrelatedProcess(t *testing.T) {
	sock := filepath.Join(shortTempDir(t), "cliamp.sock")
	// A leftover socket inode with no listener, plus a PID file pointing at a
	// live process that is not this one.
	leaveStaleSocket(t, sock)
	if err := os.WriteFile(sock+".pid", []byte(strconv.Itoa(os.Getpid())), 0o600); err != nil {
		t.Fatal(err)
	}

	server, err := NewServer(sock)
	if err != nil {
		t.Fatalf("NewServer() with a stale PID file = %v, want it to start", err)
	}
	t.Cleanup(func() { _ = server.Close() })

	pid, err := os.ReadFile(sock + ".pid")
	if err != nil {
		t.Fatal(err)
	}
	if got, want := strings.TrimSpace(string(pid)), strconv.Itoa(os.Getpid()); got != want {
		t.Fatalf("PID file = %q, want %q", got, want)
	}
}

// Two daemons starting at once must not both succeed, and the loser must not
// leave the winner unreachable on an unlinked socket.
func TestNewServerConcurrentStartsYieldOneServer(t *testing.T) {
	sock := filepath.Join(shortTempDir(t), "cliamp.sock")

	const starters = 8
	var (
		wg        sync.WaitGroup
		mu        sync.Mutex
		succeeded []*Server
	)
	start := make(chan struct{})
	for i := 0; i < starters; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			server, err := NewServer(sock)
			if err != nil {
				return
			}
			mu.Lock()
			succeeded = append(succeeded, server)
			mu.Unlock()
		}()
	}
	close(start)
	wg.Wait()

	if len(succeeded) == 0 {
		t.Fatal("no server acquired the socket")
	}
	t.Cleanup(func() {
		for _, server := range succeeded {
			_ = server.Close()
		}
	})
	if len(succeeded) != 1 {
		t.Fatalf("%d of %d starters acquired %s, want exactly 1", len(succeeded), starters, sock)
	}

	// The winner must still be reachable at the published path.
	listening, err := Listening(sock)
	if err != nil {
		t.Fatal(err)
	}
	if !listening {
		t.Fatal("socket is not accepting connections after a concurrent start")
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

// The probe-remove-rebind sequence must run under the cross-process lock, or a
// starter can delete a socket another starter just bound. This asserts the lock
// is genuinely held for that sequence: while a competing holder owns it,
// listenExclusive cannot proceed past its failed bind.
func TestListenExclusiveHoldsLockAcrossStaleReplacement(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fileutil.LockFile takes no lock on Windows")
	}
	sock := filepath.Join(shortTempDir(t), "cliamp.sock")
	leaveStaleSocket(t, sock)

	hold, err := fileutil.LockFile(sock + ".lock")
	if err != nil {
		t.Fatalf("take competing lock: %v", err)
	}

	// The worker reports the startup error, so a startup that fails after the
	// lock is released cannot be mistaken for a successful one.
	result := make(chan error, 1)
	go func() {
		server, err := NewServer(sock)
		if err == nil {
			_ = server.Close()
		}
		result <- err
	}()

	select {
	case err := <-result:
		t.Fatalf("listenExclusive proceeded while another holder owned the lock: %v", err)
	case <-time.After(250 * time.Millisecond):
	}

	if err := hold(); err != nil {
		t.Fatalf("release competing lock: %v", err)
	}

	select {
	case err := <-result:
		if err != nil {
			t.Fatalf("listenExclusive did not acquire the socket after the lock was released: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("listenExclusive did not proceed after the lock was released")
	}
}
