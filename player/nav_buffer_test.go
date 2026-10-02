package player

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/bjarneo/cliamp/internal/httpclient"
)

// startNavRead reads n bytes from r in a goroutine. started closes before the
// read begins, and result receives the bytes and the error.
func startNavRead(r io.Reader, n int) (<-chan struct{}, <-chan navReadResult) {
	started := make(chan struct{})
	result := make(chan navReadResult, 1)
	go func() {
		close(started)
		buf := make([]byte, n)
		got, err := io.ReadFull(r, buf)
		result <- navReadResult{data: string(buf[:got]), err: err}
	}()
	return started, result
}

type navReadResult struct {
	data string
	err  error
}

// assertNavReadBlocked fails when the read returns before the test lets it.
func assertNavReadBlocked(t *testing.T, result <-chan navReadResult) {
	t.Helper()
	select {
	case r := <-result:
		t.Fatalf("read returned before its data arrived: (%q, %v)", r.data, r.err)
	case <-time.After(50 * time.Millisecond):
	}
}

func TestNavBufferProgressiveRead(t *testing.T) {
	firstSent := make(chan struct{})
	release := make(chan struct{})
	var releaseOnce sync.Once
	releaseDownload := func() { releaseOnce.Do(func() { close(release) }) }
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.UserAgent(); got != httpclient.UserAgent {
			t.Errorf("User-Agent = %q, want %q", got, httpclient.UserAgent)
		}
		w.Header().Set("Content-Length", "10")
		_, _ = io.WriteString(w, "abcd")
		w.(http.Flusher).Flush()
		close(firstSent)
		<-release
		_, _ = io.WriteString(w, "efghij")
	}))
	t.Cleanup(server.Close)

	b, total, err := newNavBuffer(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = b.Close() })
	t.Cleanup(releaseDownload)
	path := b.path

	if total != 10 {
		t.Fatalf("newNavBuffer() total = %d, want 10", total)
	}
	<-firstSent

	reader := b.newReader()
	first := make([]byte, 4)
	if _, err := io.ReadFull(reader, first); err != nil {
		t.Fatalf("read first chunk: %v", err)
	}
	if got := string(first); got != "abcd" {
		t.Fatalf("first chunk = %q, want %q", got, "abcd")
	}

	started, result := startNavRead(reader, 6)
	<-started
	assertNavReadBlocked(t, result)

	releaseDownload()
	select {
	case r := <-result:
		if r.err != nil {
			t.Fatalf("read after the download continued: %v", r.err)
		}
		if r.data != "efghij" {
			t.Fatalf("read after the download continued = %q, want %q", r.data, "efghij")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("read did not unblock after requested data arrived")
	}

	// A seek starts a new reader, which replays the buffer from byte 0.
	replay := make([]byte, 2)
	if _, err := io.ReadFull(b.newReader(), replay); err != nil {
		t.Fatalf("read from a second reader: %v", err)
	}
	if got := string(replay); got != "ab" {
		t.Fatalf("second reader = %q, want %q", got, "ab")
	}
	if got := b.bytesIn.Load(); got != 10 {
		t.Fatalf("bytesIn = %d, want 10", got)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat temporary file: %v", err)
	}
	if info.Size() != 10 {
		t.Fatalf("temporary file size = %d, want 10", info.Size())
	}

	if err := b.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("temporary file still exists after Close: %v", err)
	}
}

func TestNavBufferReadsUnknownLengthToEOF(t *testing.T) {
	firstSent := make(chan struct{})
	release := make(chan struct{})
	var releaseOnce sync.Once
	releaseDownload := func() { releaseOnce.Do(func() { close(release) }) }
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "abc")
		w.(http.Flusher).Flush()
		close(firstSent)
		<-release
		_, _ = io.WriteString(w, "def")
	}))
	t.Cleanup(server.Close)

	b, total, err := newNavBuffer(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = b.Close() })
	t.Cleanup(releaseDownload)
	if total != -1 {
		t.Fatalf("newNavBuffer() total = %d, want -1", total)
	}
	<-firstSent

	result := make(chan navReadResult, 1)
	go func() {
		data, err := io.ReadAll(b.newReader())
		result <- navReadResult{data: string(data), err: err}
	}()
	assertNavReadBlocked(t, result)

	releaseDownload()
	select {
	case r := <-result:
		if r.err != nil {
			t.Fatalf("ReadAll: %v", r.err)
		}
		if r.data != "abcdef" {
			t.Fatalf("ReadAll = %q, want %q", r.data, "abcdef")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("read did not reach EOF after the download completed")
	}
}

func TestNavBufferCloseCancelsAndUnblocks(t *testing.T) {
	requestStarted := make(chan struct{})
	requestCanceled := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.(http.Flusher).Flush()
		close(requestStarted)
		<-r.Context().Done()
		close(requestCanceled)
	}))
	t.Cleanup(server.Close)

	b, _, err := newNavBuffer(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	path := b.path
	<-requestStarted

	// 2 readers stand for the old and the replacement ffmpeg of a seek.
	var results []<-chan navReadResult
	for range 2 {
		started, result := startNavRead(b.newReader(), 1)
		<-started
		results = append(results, result)
	}
	for _, result := range results {
		assertNavReadBlocked(t, result)
	}

	const closeCallers = 8
	closeErrs := make(chan error, closeCallers)
	var closeWG sync.WaitGroup
	for range closeCallers {
		closeWG.Add(1)
		go func() {
			defer closeWG.Done()
			closeErrs <- b.Close()
		}()
	}
	closeWG.Wait()
	close(closeErrs)
	for err := range closeErrs {
		if err != nil {
			t.Errorf("Close: %v", err)
		}
	}
	if err := b.Close(); err != nil {
		t.Fatalf("Close after concurrent calls: %v", err)
	}
	for i, result := range results {
		select {
		case r := <-result:
			if !errors.Is(r.err, errNavBufferClosed) {
				t.Fatalf("blocked read %d error = %v, want %v", i, r.err, errNavBufferClosed)
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("Close did not unblock read %d", i)
		}
	}
	select {
	case <-requestCanceled:
	case <-time.After(2 * time.Second):
		t.Fatal("Close did not cancel the HTTP request")
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("temporary file still exists after Close: %v", err)
	}
	select {
	case <-b.downloadDone:
	default:
		t.Fatal("Close returned before the download goroutine stopped")
	}
}

func TestNavBufferStalls(t *testing.T) {
	tests := []struct {
		name string
		sent string // bytes the server sends before it stalls
	}{
		{name: "before any data"},
		{name: "after partial data", sent: "ab"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusOK)
				_, _ = io.WriteString(w, tt.sent)
				w.(http.Flusher).Flush()
				<-r.Context().Done()
			}))
			t.Cleanup(server.Close)

			b, _, err := newNavBuffer(server.URL)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = b.Close() })
			b.stallTimeout = 25 * time.Millisecond

			got, err := io.ReadAll(b.newReader())
			if !errors.Is(err, errNavBufferReadStalled) {
				t.Fatalf("read error = %v, want %v", err, errNavBufferReadStalled)
			}
			if string(got) != tt.sent {
				t.Fatalf("read before the stall = %q, want %q", got, tt.sent)
			}
		})
	}
}

func TestNavBufferTempfileInitializationErrorCancelsRequest(t *testing.T) {
	missingTempDir := filepath.Join(t.TempDir(), "missing")
	t.Setenv("TMPDIR", missingTempDir)
	t.Setenv("TMP", missingTempDir)
	t.Setenv("TEMP", missingTempDir)

	requestCanceled := make(chan struct{})
	stop := make(chan struct{})
	var stopOnce sync.Once
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.(http.Flusher).Flush()
		select {
		case <-r.Context().Done():
			close(requestCanceled)
		case <-stop:
		}
	}))
	t.Cleanup(server.Close)
	t.Cleanup(func() { stopOnce.Do(func() { close(stop) }) })

	if b, _, err := newNavBuffer(server.URL); err == nil {
		_ = b.Close()
		t.Fatal("newNavBuffer() error = nil, want tempfile error")
	}
	select {
	case <-requestCanceled:
	case <-time.After(2 * time.Second):
		t.Fatal("tempfile initialization error did not cancel the HTTP request")
	}
}

func TestNavBufferSegmentsConcatenatesInOrder(t *testing.T) {
	segments := map[string]string{
		"/init.mp4": "INIT",
		"/seg1.m4s": "AAAA",
		"/seg2.m4s": "BBBBBB",
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, ok := segments[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "audio/mp4")
		_, _ = io.WriteString(w, body)
	}))
	defer server.Close()

	nb, contentLen, err := newNavBufferSegments([]string{
		server.URL + "/init.mp4",
		server.URL + "/seg1.m4s",
		server.URL + "/seg2.m4s",
	})
	if err != nil {
		t.Fatalf("newNavBufferSegments: %v", err)
	}
	defer nb.Close()

	if contentLen != -1 {
		t.Errorf("contentLength = %d, want -1 (unknown)", contentLen)
	}

	got, err := io.ReadAll(nb.newReader())
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if want := "INITAAAABBBBBB"; string(got) != want {
		t.Errorf("concatenated bytes = %q, want %q", got, want)
	}
}

func TestNavBufferSegmentsSurfacesMidStreamError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/init.mp4" {
			_, _ = io.WriteString(w, "INIT")
			return
		}
		http.Error(w, "expired", http.StatusForbidden)
	}))
	defer server.Close()

	nb, _, err := newNavBufferSegments([]string{server.URL + "/init.mp4", server.URL + "/gone.m4s"})
	if err != nil {
		t.Fatalf("newNavBufferSegments: %v", err)
	}
	defer nb.Close()

	if _, err := io.ReadAll(nb.newReader()); err == nil {
		t.Fatal("expected mid-stream segment error to surface on read")
	}
}

func TestNavBufferSegmentsRejectsEmptyList(t *testing.T) {
	if _, _, err := newNavBufferSegments(nil); err == nil {
		t.Fatal("expected error for empty segment list")
	}
}
