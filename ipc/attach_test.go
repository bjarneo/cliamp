package ipc

import (
	"errors"
	"net"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// attachRecorder is a minimal session host: it echoes one byte back over the
// connection it takes over.
type attachRecorder struct {
	mu       sync.Mutex
	requests []V2Request
	done     chan struct{}
}

func newAttachRecorder() *attachRecorder {
	return &attachRecorder{done: make(chan struct{}, 1)}
}

func (a *attachRecorder) HandleAttach(conn net.Conn, request V2Request) {
	a.mu.Lock()
	a.requests = append(a.requests, request)
	a.mu.Unlock()
	_ = WriteV2Response(conn, V2Response{ID: request.ID, OK: true})
	// Raw bytes after the acknowledgment: the session speaks its own protocol.
	_, _ = conn.Write([]byte{0x7F})
	a.done <- struct{}{}
}

func (a *attachRecorder) count() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return len(a.requests)
}

func TestAttachHandsOffTheConnection(t *testing.T) {
	sock := filepath.Join(shortTempDir(t), "cliamp.sock")
	server, err := NewServer(sock)
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	recorder := newAttachRecorder()
	server.SetAttachHandler(recorder)

	conn, response, err := DialAttach(sock, V2Request{ID: []byte(`"attach"`), Params: []byte(`{"width":80,"height":24}`)})
	if err != nil {
		t.Fatalf("DialAttach: %v", err)
	}
	defer conn.Close()
	if !response.OK {
		t.Fatalf("attach response = %+v, want ok", response)
	}

	// The bytes the handler wrote after the acknowledgment must reach the
	// client: buffering the handshake would have swallowed them.
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	var one [1]byte
	if _, err := conn.Read(one[:]); err != nil {
		t.Fatalf("read session byte: %v", err)
	}
	if one[0] != 0x7F {
		t.Errorf("session byte = %#x, want 0x7f", one[0])
	}
	if recorder.count() != 1 {
		t.Errorf("handler calls = %d, want 1", recorder.count())
	}
}

// A cliamp that owns a real terminal registers no handler, and an attach must
// say so rather than hang or look successful.
func TestAttachWithoutHandlerReportsUnavailable(t *testing.T) {
	sock := filepath.Join(shortTempDir(t), "cliamp.sock")
	server, err := NewServer(sock)
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()

	_, _, err = DialAttach(sock, V2Request{ID: []byte(`"attach"`), Params: []byte(`{"width":80,"height":24}`)})
	var protocolErr *V2Error
	if !errors.As(err, &protocolErr) {
		t.Fatalf("err = %v, want a protocol error", err)
	}
	if protocolErr.Code != V2ErrorCodeUnavailable {
		t.Errorf("code = %q, want %q", protocolErr.Code, V2ErrorCodeUnavailable)
	}
}

func TestDialAttachReportsMissingSocket(t *testing.T) {
	sock := filepath.Join(shortTempDir(t), "missing.sock")
	if _, _, err := DialAttach(sock, V2Request{ID: []byte(`1`)}); !errors.Is(err, ErrNotRunning) {
		t.Errorf("err = %v, want ErrNotRunning", err)
	}
}
