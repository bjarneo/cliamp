package ipc

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
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
	// Keep the socket directory short on every platform so sun_path stays
	// under the 107-byte Unix limit even with long subtest names or GOTMPDIR.
	base := ""
	if runtime.GOOS != "windows" {
		base = "/tmp"
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
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	ln.(*net.UnixListener).SetUnlinkOnClose(false)
	if err := ln.Close(); err != nil {
		t.Fatal(err)
	}
	server, err := NewServer(sock)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" {
		dirInfo, err := os.Stat(dir)
		if err != nil {
			t.Fatalf("stat socket dir: %v", err)
		}
		if got := dirInfo.Mode().Perm(); got != 0o700 {
			t.Fatalf("socket dir perm = %o, want 700", got)
		}
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

// An old server must not remove the replacement socket or PID file if
// another instance takes over the pathname before the old Close runs.
func TestOldServerClosePreservesReplacement(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("cannot replace an open Unix socket pathname on Windows")
	}
	sock := filepath.Join(shortTempDir(t), "cliamp.sock")
	old, err := NewServer(sock)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = old.Close() })

	// Simulate stale-path replacement while the old listener is shutting
	// down. The old listener has disabled net.UnixListener auto-unlink.
	if err := old.listener.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(sock); err != nil {
		t.Fatal(err)
	}
	replacement, err := NewServer(sock)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = replacement.Close() })
	replacementPID, err := os.Lstat(sock + ".pid")
	if err != nil {
		t.Fatal(err)
	}

	_ = old.Close()
	listening, err := Listening(sock)
	if err != nil || !listening {
		t.Fatalf("old Close displaced replacement socket: listening=%v, err=%v", listening, err)
	}
	currentPID, err := os.Lstat(sock + ".pid")
	if err != nil || !os.SameFile(currentPID, replacementPID) {
		t.Fatalf("old Close displaced replacement PID: current=%v, err=%v", currentPID, err)
	}
}

// Even if the socket is still ours, cleanup must not remove a PID file
// replaced independently by another process.
func TestOldServerClosePreservesForeignPIDFile(t *testing.T) {
	sock := filepath.Join(shortTempDir(t), "cliamp.sock")
	server, err := NewServer(sock)
	if err != nil {
		t.Fatal(err)
	}
	pidPath := sock + ".pid"
	if err := os.Remove(pidPath); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(pidPath, []byte("foreign-pid"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := server.Close(); err != nil {
		t.Fatal(err)
	}
	if got, err := os.ReadFile(pidPath); err != nil || string(got) != "foreign-pid" {
		t.Fatalf("Close changed foreign PID file: content=%q, err=%v", got, err)
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

	if _, err := NewServer(sock); err == nil || !strings.Contains(err.Error(), "already running") || strings.Count(err.Error(), "ipc:") != 1 {
		t.Fatalf("NewServer() against a live server = %v, want already running with one ipc: prefix", err)
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
	if runtime.GOOS == "windows" {
		t.Skip("fileutil.LockFile takes no lock on Windows")
	}
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

func TestNewServerSocketSecurity(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX permissions and symlink tests require Unix")
	}

	tests := []struct {
		name    string
		setup   func(t *testing.T, base string) (sock string, verify func(t *testing.T))
		wantErr string
	}{
		{
			name: "refuses regular file at socket path",
			setup: func(t *testing.T, base string) (string, func(t *testing.T)) {
				sock := filepath.Join(base, "cliamp.sock")
				if err := os.WriteFile(sock, []byte("keep"), 0o600); err != nil {
					t.Fatal(err)
				}
				return sock, func(t *testing.T) {
					data, err := os.ReadFile(sock)
					if err != nil || string(data) != "keep" {
						t.Fatalf("regular file was modified or removed: %q, %v", data, err)
					}
				}
			},
			wantErr: "non-socket",
		},
		{
			name: "refuses symlink at socket path",
			setup: func(t *testing.T, base string) (string, func(t *testing.T)) {
				target := filepath.Join(base, "victim")
				if err := os.WriteFile(target, []byte("keep"), 0o600); err != nil {
					t.Fatal(err)
				}
				sock := filepath.Join(base, "cliamp.sock")
				if err := os.Symlink(target, sock); err != nil {
					t.Fatal(err)
				}
				return sock, func(t *testing.T) {
					data, err := os.ReadFile(target)
					if err != nil || string(data) != "keep" {
						t.Fatalf("symlink target was modified or removed: %q, %v", data, err)
					}
				}
			},
			wantErr: "non-socket",
		},
		{
			name: "refuses symlinked socket directory",
			setup: func(t *testing.T, base string) (string, func(t *testing.T)) {
				realDir := filepath.Join(base, "real")
				if err := os.MkdirAll(realDir, 0o700); err != nil {
					t.Fatal(err)
				}
				linkDir := filepath.Join(base, "link")
				if err := os.Symlink(realDir, linkDir); err != nil {
					t.Fatal(err)
				}
				return filepath.Join(linkDir, "cliamp.sock"), nil
			},
			wantErr: "symlink",
		},
		{
			name: "refuses symlinked pid file",
			setup: func(t *testing.T, base string) (string, func(t *testing.T)) {
				target := filepath.Join(base, "target")
				if err := os.WriteFile(target, []byte("keep"), 0o600); err != nil {
					t.Fatal(err)
				}
				sock := filepath.Join(base, "cliamp.sock")
				if err := os.Symlink(target, sock+".pid"); err != nil {
					t.Fatal(err)
				}
				return sock, func(t *testing.T) {
					data, err := os.ReadFile(target)
					if err != nil || string(data) != "keep" {
						t.Fatalf("pid symlink target was modified: %q, %v", data, err)
					}
				}
			},
			wantErr: "pid file",
		},
		{
			name: "tightens existing directory permissions to 0700",
			setup: func(t *testing.T, base string) (string, func(t *testing.T)) {
				sub := filepath.Join(base, "open")
				if err := os.MkdirAll(sub, 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.Chmod(sub, 0o755); err != nil {
					t.Fatal(err)
				}
				return filepath.Join(sub, "cliamp.sock"), func(t *testing.T) {
					info, err := os.Stat(sub)
					if err != nil {
						t.Fatal(err)
					}
					if info.Mode().Perm() != 0o700 {
						t.Fatalf("dir perm = %o, want 700", info.Mode().Perm())
					}
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			base := shortTempDir(t)
			sock, verify := tt.setup(t, base)
			server, err := NewServer(sock)
			if server != nil {
				t.Cleanup(func() { _ = server.Close() })
			}
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("NewServer() error = %v, want substring %q", err, tt.wantErr)
				}
			} else if err != nil {
				t.Fatalf("NewServer() error = %v", err)
			}
			if verify != nil {
				verify(t)
			}
		})
	}
}

func TestNewServerWaitsForSocketLock(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fileutil.LockFile does not lock on Windows")
	}
	dir := shortTempDir(t)
	sock := filepath.Join(dir, "cliamp.sock")
	unlock, err := fileutil.LockFile(sock + ".lock")
	if err != nil {
		t.Fatal(err)
	}

	started := make(chan error, 1)
	var server *Server
	go func() {
		var startErr error
		server, startErr = NewServer(sock)
		started <- startErr
	}()

	select {
	case err := <-started:
		if server != nil {
			_ = server.Close()
		}
		t.Fatalf("NewServer() returned while lock was held: %v", err)
	case <-time.After(50 * time.Millisecond):
	}

	if err := unlock(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-started:
		if err != nil {
			t.Fatalf("NewServer() after unlock error = %v", err)
		}
		t.Cleanup(func() { _ = server.Close() })
	case <-time.After(2 * time.Second):
		t.Fatal("NewServer() did not resume after lock release")
	}
}

func TestDefaultSocketPathFallbackUsesUserDir(t *testing.T) {
	tests := []struct {
		name    string
		uid     int
		wantSub string
	}{
		{name: "unix uid", uid: 1000, wantSub: filepath.Join(os.TempDir(), "cliamp-1000", "cliamp.sock")},
		{name: "root uid", uid: 0, wantSub: filepath.Join(os.TempDir(), "cliamp-0", "cliamp.sock")},
		{name: "windows negative uid", uid: -1, wantSub: "cliamp.sock"},
	}

	failDir := func() (string, error) { return "", errors.New("no home") }
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := defaultSocketPath(failDir, tt.uid)
			if tt.uid >= 0 {
				if got != tt.wantSub {
					t.Fatalf("defaultSocketPath() = %q, want %q", got, tt.wantSub)
				}
				return
			}
			if filepath.Dir(got) == filepath.Clean(os.TempDir()) {
				t.Fatalf("fallback socket %q placed directly in temp dir", got)
			}
			if filepath.Base(got) != tt.wantSub {
				t.Fatalf("base = %q, want %q", filepath.Base(got), tt.wantSub)
			}
		})
	}
}

func TestPeerUIDAllowed(t *testing.T) {
	tests := []struct {
		name    string
		peerUID uint32
		euid    int
		wantErr bool
	}{
		{name: "matching uid", peerUID: 1000, euid: 1000},
		{name: "matching root", peerUID: 0, euid: 0},
		{name: "mismatched uid", peerUID: 1001, euid: 1000, wantErr: true},
		{name: "negative euid", peerUID: 0, euid: -1, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := peerUIDAllowed(tt.peerUID, tt.euid)
			if (err != nil) != tt.wantErr {
				t.Fatalf("peerUIDAllowed(%d, %d) error = %v, wantErr %v", tt.peerUID, tt.euid, err, tt.wantErr)
			}
		})
	}
}

func TestNewServerWithOptionsAvoidsStartupRace(t *testing.T) {
	sock := filepath.Join(shortTempDir(t), "cliamp.sock")
	broker := NewBroker()
	t.Cleanup(broker.Close)
	ops := NewOperationRegistry(Operation{Name: "custom.ping"})
	dispatcher := V2DispatcherFunc(func(_ context.Context, req V2Request) (V2Result, *V2Error) {
		return V2Result{Result: json.RawMessage(`{"pong":true}`)}, nil
	})

	server, err := NewServerWithOptions(
		sock,
		WithBroker(broker),
		WithOperationRegistry(ops),
		WithV2Dispatcher(dispatcher),
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = server.Close() })

	response, err := SendV2(sock, V2Request{ID: json.RawMessage(`"opt"`), Operation: "custom.ping"})
	if err != nil {
		t.Fatal(err)
	}
	if !response.OK || string(response.Result) != `{"pong":true}` {
		t.Fatalf("response = %#v", response)
	}
}

func TestServerCapsConcurrentConnections(t *testing.T) {
	sock := filepath.Join(shortTempDir(t), "cliamp.sock")
	server, err := NewServer(sock)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = server.Close() })

	conns := make([]net.Conn, 0, maxConcurrentConns)
	for range maxConcurrentConns {
		c, err := dialSocket(sock, time.Second)
		if err != nil {
			t.Fatalf("dial %d: %v", len(conns), err)
		}
		conns = append(conns, c)
	}
	defer func() {
		for _, c := range conns {
			_ = c.Close()
		}
	}()

	// Wait until the server registers all active connections.
	deadline := time.Now().Add(2 * time.Second)
	for {
		server.connMu.Lock()
		n := len(server.conns)
		server.connMu.Unlock()
		if n >= maxConcurrentConns {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("active conns = %d, want %d", n, maxConcurrentConns)
		}
		time.Sleep(5 * time.Millisecond)
	}

	extra, err := dialSocket(sock, time.Second)
	if err == nil {
		defer extra.Close()
		_ = extra.SetReadDeadline(time.Now().Add(time.Second))
		var buf [1]byte
		if n, readErr := extra.Read(buf[:]); readErr == nil || n > 0 {
			t.Fatalf("connection past limit stayed open: n=%d, err=%v", n, readErr)
		}
	}

	_ = conns[0].Close()
	conns = conns[1:]
	deadline = time.Now().Add(2 * time.Second)
	for {
		resp, err := SendV2(sock, V2Request{ID: json.RawMessage(`1`), Method: "capabilities"})
		if err == nil && resp.OK {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("SendV2 after freeing slot failed: %v", err)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestServerClosesAfterConsecutiveEmptyLines(t *testing.T) {
	sock := filepath.Join(shortTempDir(t), "cliamp.sock")
	server, err := NewServer(sock)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = server.Close() })

	// Below limit resets on a real request.
	t.Run("below limit survives", func(t *testing.T) {
		conn, err := dialSocket(sock, time.Second)
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
		payload := strings.Repeat("\n", maxConsecutiveEmpty-1) + `{"version":2,"id":1,"method":"capabilities"}` + "\n"
		if _, err := conn.Write([]byte(payload)); err != nil {
			t.Fatal(err)
		}
		scanner := newFrameScanner(conn)
		if !scanner.Scan() {
			t.Fatalf("expected response before empty limit: %v", scanner.Err())
		}
	})

	t.Run("at limit disconnects", func(t *testing.T) {
		conn, err := dialSocket(sock, time.Second)
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
		if _, err := conn.Write([]byte(strings.Repeat("\n", maxConsecutiveEmpty))); err != nil {
			t.Fatal(err)
		}
		_ = conn.SetReadDeadline(time.Now().Add(time.Second))
		var buf [1]byte
		if n, err := conn.Read(buf[:]); err == nil || n > 0 {
			t.Fatalf("connection stayed open after %d empty lines: n=%d, err=%v", maxConsecutiveEmpty, n, err)
		}
	})
}

func TestStreamSubscriptionExitsCleanlyOnBrokerClose(t *testing.T) {
	broker := NewBroker()
	sock := filepath.Join(shortTempDir(t), "cliamp.sock")
	server, err := NewServerWithBroker(sock, broker)
	if err != nil {
		t.Fatal(err)
	}

	stream, err := SubscribeV2(sock, json.RawMessage(`"sub"`), []string{"runtime.state"})
	if err != nil {
		_ = server.Close()
		t.Fatal(err)
	}
	defer stream.Close()

	// Closing the broker ends the subscription while the client stays silent.
	broker.Close()
	if _, err := stream.Next(); err == nil {
		t.Fatal("stream stayed open after broker close")
	}

	closed := make(chan error, 1)
	go func() { closed <- server.Close() }()
	select {
	case err := <-closed:
		if err != nil {
			t.Fatalf("server.Close() error = %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("server.Close() blocked waiting for subscription goroutine")
	}
}

func TestStreamBandsValidation(t *testing.T) {
	tests := []struct {
		name       string
		result     json.RawMessage
		wantOutput string
		wantErr    string
	}{
		{
			name:       "forwards valid object directly",
			result:     json.RawMessage(`{"ok":true,"visualizer":"Wave","bands":[0.25,0.75]}`),
			wantOutput: `{"ok":true,"visualizer":"Wave","bands":[0.25,0.75]}`,
		},
		{
			name:    "rejects non-object spectrum payload",
			result:  json.RawMessage(`[1,2,3]`),
			wantErr: "invalid spectrum payload",
		},
		{
			name:    "rejects empty spectrum payload",
			result:  nil,
			wantErr: "invalid spectrum payload",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sock := filepath.Join(shortTempDir(t), "cliamp.sock")
			server, err := NewServerWithOptions(sock, WithV2Dispatcher(V2DispatcherFunc(func(_ context.Context, _ V2Request) (V2Result, *V2Error) {
				return V2Result{Result: tt.result}, nil
			})))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = server.Close() })

			ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
			defer cancel()
			var buf bytes.Buffer
			err = StreamBands(ctx, sock, 5*time.Millisecond, &buf)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("StreamBands() error = %v, want %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("StreamBands() error = %v", err)
			}
			if !strings.Contains(buf.String(), fmt.Sprintf("%s\n", tt.wantOutput)) {
				t.Fatalf("output = %q, want %q", buf.String(), tt.wantOutput)
			}
		})
	}
}

// Both ordinary startup and stale-socket recovery must respect the same
// lock, including the period before the first bind.
func assertNewServerWaitsForSocketLock(t *testing.T, stale bool) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("fileutil.LockFile takes no lock on Windows")
	}
	sock := filepath.Join(shortTempDir(t), "cliamp.sock")
	if stale {
		leaveStaleSocket(t, sock)
	}

	release, err := fileutil.LockFile(sock + ".lock")
	if err != nil {
		t.Fatalf("take competing lock: %v", err)
	}
	t.Cleanup(func() {
		if release != nil {
			_ = release()
		}
	})

	started := make(chan struct{})
	result := make(chan error, 1)
	go func() {
		close(started)
		server, err := NewServer(sock)
		if err == nil {
			_ = server.Close()
		}
		result <- err
	}()
	<-started

	select {
	case err := <-result:
		t.Fatalf("NewServer proceeded while another holder owned the lock: %v", err)
	case <-time.After(250 * time.Millisecond):
	}

	if err := release(); err != nil {
		t.Fatalf("release competing lock: %v", err)
	}
	release = nil

	select {
	case err := <-result:
		if err != nil {
			t.Fatalf("NewServer failed after the lock was released: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("NewServer did not proceed after the lock was released")
	}
}

func TestNewServerWaitsForSocketLockWithStaleSocket(t *testing.T) {
	assertNewServerWaitsForSocketLock(t, true)
}

func TestNewServerWaitsForSocketLockWithoutStaleSocket(t *testing.T) {
	assertNewServerWaitsForSocketLock(t, false)
}
