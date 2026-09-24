package player

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"
)

func TestIsHLS(t *testing.T) {
	if !isHLS(".m3u8") {
		t.Error(".m3u8 should be HLS")
	}
	for _, ext := range []string{".mp3", ".m3u", ".aac", ""} {
		if isHLS(ext) {
			t.Errorf("%q should not be HLS", ext)
		}
	}
}

func TestNeedsFFmpeg(t *testing.T) {
	tests := []struct {
		ext  string
		want bool
	}{
		{ext: ".aac", want: true},
		{ext: ".aacp", want: true},
		{ext: ".opus", want: true},
		{ext: ".mp3", want: false},
		{ext: ".ogg", want: false},
	}

	for _, tt := range tests {
		if got := needsFFmpeg(tt.ext); got != tt.want {
			t.Errorf("needsFFmpeg(%q) = %v, want %v", tt.ext, got, tt.want)
		}
	}
}

func TestSupportedExtsIncludesAACP(t *testing.T) {
	if !SupportedExts[".aacp"] {
		t.Fatal("SupportedExts[.aacp] = false, want true")
	}
}

func TestOpenSourceClassifiesHTTPResponse(t *testing.T) {
	tests := []struct {
		name     string
		chunked  bool
		icy      bool
		prefetch bool
		live     bool
	}{
		{name: "chunked radio", chunked: true, icy: true, prefetch: true, live: true},
		{name: "finite chunked file", chunked: true, prefetch: true},
		{name: "content length"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "audio/mpeg")
				if tt.icy {
					w.Header().Set("Icy-Name", "Test Radio")
				}
				if tt.chunked {
					w.WriteHeader(http.StatusOK)
					w.(http.Flusher).Flush()
					<-r.Context().Done()
					return
				}
				_, _ = w.Write([]byte("finite audio"))
			}))
			defer server.Close()

			src, err := openSource(context.Background(), server.URL, nil)
			if err != nil {
				t.Fatalf("openSource: %v", err)
			}
			defer src.body.Close()
			if src.prefetch != tt.prefetch || src.live != tt.live {
				t.Fatalf("prefetch/live = %v/%v, want %v/%v (Content-Length %d)", src.prefetch, src.live, tt.prefetch, tt.live, src.contentLength)
			}
		})
	}
}

// Returning headers must not detach body reads from the source lifetime.
func TestOpenSourceCancellationAfterHeaders(t *testing.T) {
	requestCanceled := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "audio/mpeg")
		w.WriteHeader(http.StatusOK)
		w.(http.Flusher).Flush()
		<-r.Context().Done()
		close(requestCanceled)
	}))
	defer server.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	src, err := openSource(ctx, server.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer src.body.Close()

	readStarted := make(chan struct{})
	body := &notifyingReadCloser{ReadCloser: src.body, started: readStarted}
	decoded := make(chan error, 1)
	go func() {
		_, _, err := decodeWithExt(ctx, body, ".mp3", server.URL, 44100, 16)
		decoded <- err
	}()
	<-readStarted
	cancel()
	select {
	case err := <-decoded:
		if err == nil {
			t.Fatal("native decoder accepted a canceled, empty source")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("native decoder remained blocked after source cancellation")
	}
	select {
	case <-requestCanceled:
	case <-time.After(2 * time.Second):
		t.Fatal("source cancellation did not close the HTTP request")
	}
}

type notifyingReadCloser struct {
	io.ReadCloser
	started chan struct{}
	once    sync.Once
}

func (r *notifyingReadCloser) Read(p []byte) (int, error) {
	r.once.Do(func() { close(r.started) })
	return r.ReadCloser.Read(p)
}

func TestSSHSourceCancellationAfterOpen(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses POSIX shell fixture")
	}
	dir := t.TempDir()
	writeExecutable(t, filepath.Join(dir, "ssh"), "#!/bin/sh\nprintf x\nexec sleep 30\n")
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	src, err := openSSHSource(ctx, "ssh://example.test/music/track.mp3")
	if err != nil {
		t.Fatal(err)
	}
	defer src.body.Close()
	if _, err := io.ReadFull(src.body, make([]byte, 1)); err != nil {
		t.Fatal(err)
	}
	readDone := make(chan error, 1)
	go func() {
		_, err := src.body.Read(make([]byte, 1))
		readDone <- err
	}()
	cancel()
	select {
	case err := <-readDone:
		if err == nil {
			t.Fatal("SSH read ignored source cancellation")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("SSH read remained blocked after source cancellation")
	}
	src.body.Close()
	if src.body.(*sshReadCloser).cmd.ProcessState == nil {
		t.Fatal("SSH child was not reaped")
	}
}
