package player

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestNavBufferProgressiveReadAndSeek(t *testing.T) {
	firstSent := make(chan struct{})
	release := make(chan struct{})
	var releaseOnce sync.Once
	releaseDownload := func() { releaseOnce.Do(func() { close(release) }) }
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Length", "10")
		w.Header().Set("Content-Type", "audio/test")
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
	if got := b.ContentType(); got != "audio/test" {
		t.Fatalf("ContentType() = %q, want %q", got, "audio/test")
	}
	<-firstSent

	first := make([]byte, 4)
	if _, err := io.ReadFull(b, first); err != nil {
		t.Fatalf("read first chunk: %v", err)
	}
	if got := string(first); got != "abcd" {
		t.Fatalf("first chunk = %q, want %q", got, "abcd")
	}

	seekStarted := make(chan struct{})
	seekResult := make(chan error, 1)
	go func() {
		close(seekStarted)
		pos, err := b.Seek(-2, io.SeekEnd)
		if err == nil && pos != 8 {
			err = errors.New("seek returned wrong position")
		}
		seekResult <- err
	}()
	<-seekStarted
	waitForNavBufferCursorLock(t, b)
	select {
	case err := <-seekResult:
		t.Fatalf("Seek returned before requested data arrived: %v", err)
	default:
	}

	releaseDownload()
	select {
	case err := <-seekResult:
		if err != nil {
			t.Fatalf("Seek(-2, SeekEnd): %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Seek did not unblock after requested data arrived")
	}

	last := make([]byte, 2)
	if _, err := io.ReadFull(b, last); err != nil {
		t.Fatalf("read after progressive seek: %v", err)
	}
	if got := string(last); got != "ij" {
		t.Fatalf("read after progressive seek = %q, want %q", got, "ij")
	}
	if _, err := b.Seek(1, io.SeekStart); err != nil {
		t.Fatalf("seek backward: %v", err)
	}
	backward := make([]byte, 2)
	if _, err := io.ReadFull(b, backward); err != nil {
		t.Fatalf("read after backward seek: %v", err)
	}
	if got := string(backward); got != "bc" {
		t.Fatalf("read after backward seek = %q, want %q", got, "bc")
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

func TestNavBufferSeekEndWaitsForUnknownLength(t *testing.T) {
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

	seekResult := make(chan struct {
		pos int64
		err error
	}, 1)
	go func() {
		pos, err := b.Seek(-2, io.SeekEnd)
		seekResult <- struct {
			pos int64
			err error
		}{pos: pos, err: err}
	}()
	waitForNavBufferCursorLock(t, b)
	select {
	case result := <-seekResult:
		t.Fatalf("SeekEnd returned before unknown-length download completed: (%d, %v)", result.pos, result.err)
	default:
	}

	releaseDownload()
	select {
	case result := <-seekResult:
		if result.err != nil {
			t.Fatalf("Seek(-2, SeekEnd): %v", result.err)
		}
		if result.pos != 4 {
			t.Fatalf("Seek(-2, SeekEnd) = %d, want 4", result.pos)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("SeekEnd did not unblock after download completed")
	}

	last := make([]byte, 2)
	if _, err := io.ReadFull(b, last); err != nil {
		t.Fatalf("read after SeekEnd: %v", err)
	}
	if got := string(last); got != "ef" {
		t.Fatalf("read after SeekEnd = %q, want %q", got, "ef")
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

	readStarted := make(chan struct{})
	readResult := make(chan error, 1)
	go func() {
		close(readStarted)
		_, err := b.Read(make([]byte, 1))
		readResult <- err
	}()
	<-readStarted
	waitForNavBufferCursorLock(t, b)

	seekResult := make(chan error, 1)
	go func() {
		_, err := b.Seek(1, io.SeekStart)
		seekResult <- err
	}()

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
	select {
	case err := <-readResult:
		if !errors.Is(err, errNavBufferClosed) {
			t.Fatalf("blocked Read error = %v, want %v", err, errNavBufferClosed)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Close did not unblock Read")
	}
	select {
	case err := <-seekResult:
		if !errors.Is(err, errNavBufferClosed) {
			t.Fatalf("blocked Seek error = %v, want %v", err, errNavBufferClosed)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Close did not unblock Seek")
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
		wait func(*navBuffer) error
		want string
	}{
		{
			name: "read",
			wait: func(b *navBuffer) error {
				_, err := b.Read(make([]byte, 1))
				return err
			},
			want: "read stalled waiting for data",
		},
		{
			name: "seek",
			wait: func(b *navBuffer) error {
				_, err := b.Seek(1, io.SeekStart)
				return err
			},
			want: "seek stalled waiting for data",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusOK)
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

			if err := tt.wait(b); err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("wait error = %v, want containing %q", err, tt.want)
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

func waitForNavBufferCursorLock(t *testing.T, b *navBuffer) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for b.readMu.TryLock() {
		b.readMu.Unlock()
		if time.Now().After(deadline) {
			t.Fatal("cursor operation did not start")
		}
		runtime.Gosched()
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
	if got := nb.ContentType(); got != "audio/mp4" {
		t.Errorf("ContentType = %q, want audio/mp4", got)
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
