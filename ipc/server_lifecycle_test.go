package ipc

import (
	"bytes"
	"context"
	"encoding/json"
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

func shortTempDir(t *testing.T) string {
	t.Helper()
	if runtime.GOOS != "darwin" {
		return t.TempDir()
	}
	dir, err := os.MkdirTemp("/tmp", "c")
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
