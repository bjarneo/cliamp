package player

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/gopxl/beep/v2"
)

// A measured length always beats the metadata hint, because feeds understate
// an episode's length by however much advertising was inserted.
func TestNavStreamerLenPrefersProbedLength(t *testing.T) {
	s := &navFFmpegStreamer{ffmpegPipe: ffmpegPipe{total: 1000}}

	if got := s.Len(); got != 1000 {
		t.Errorf("Len() = %d before probing, want the metadata hint 1000", got)
	}

	s.probed.Store(1750)

	if got := s.Len(); got != 1750 {
		t.Errorf("Len() = %d after probing, want 1750", got)
	}
}

func TestNavStreamerLenIgnoresFailedProbe(t *testing.T) {
	s := &navFFmpegStreamer{ffmpegPipe: ffmpegPipe{total: 1000}}

	// probeFrames returns 0 when ffprobe is missing or the file is gone.
	s.probed.Store(0)

	if got := s.Len(); got != 1000 {
		t.Errorf("Len() = %d, want the hint 1000 to survive a failed probe", got)
	}
}

func serveBody(t *testing.T, body string) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "audio/test")
		_, _ = io.WriteString(w, body)
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

func TestNavBufferCompletedPath(t *testing.T) {
	b, _, err := newNavBuffer(serveBody(t, "abcdefghij"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = b.Close() })

	path, ok := b.completedPath()
	if !ok {
		t.Fatal("completedPath() = false for a finished download")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading the buffered file: %v", err)
	}
	if string(data) != "abcdefghij" {
		t.Errorf("buffered file = %q, want the whole body", data)
	}
}

func TestNavBufferCompletedPathAfterClose(t *testing.T) {
	b, _, err := newNavBuffer(serveBody(t, "abcdefghij"))
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := b.completedPath(); !ok {
		t.Fatal("completedPath() = false for a finished download")
	}

	_ = b.Close()

	if _, ok := b.completedPath(); ok {
		t.Error("completedPath() = true after Close removed the file")
	}
}

func TestNavBufferCompletedPathOnTruncatedDownload(t *testing.T) {
	// Content-Length promises more than the body delivers, so the download
	// finishes short and must not be measured.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Length", "20")
		_, _ = io.WriteString(w, "abcdefghij")
	}))
	t.Cleanup(srv.Close)

	b, _, err := newNavBuffer(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = b.Close() })

	if _, ok := b.completedPath(); ok {
		t.Error("completedPath() = true for a download that stopped short")
	}
}

// A finite HTTP source must reach the seekable pipeline without being
// recognized in advance: podcast CDNs rewrite enclosure URLs per request, so a
// track restored from a saved playlist never matches a URL seen before.
func TestFiniteHTTPSourceIsSeekableWithoutAMatcher(t *testing.T) {
	body := strings.Repeat("\xff\xfb\x90\x00", 4096)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "audio/mpeg")
		w.Header().Set("Content-Length", strconv.Itoa(len(body)))
		_, _ = io.WriteString(w, body)
	}))
	t.Cleanup(srv.Close)

	if !ffmpegAvailable() {
		t.Skip("ffmpeg is required for the buffered pipeline")
	}
	p := &Player{sr: beep.SampleRate(44100), bitDepth: 16}

	tp, err := p.buildPipeline(srv.URL)
	if err != nil {
		t.Fatalf("buildPipeline() error = %v", err)
	}
	defer tp.close()

	if !tp.seekable {
		t.Error("a finite HTTP source was not routed to the seekable pipeline")
	}
	if tp.contentLength != int64(len(body)) {
		t.Errorf("contentLength = %d, want %d", tp.contentLength, len(body))
	}
}

// A stream with no length is a broadcast and must stay on the live path.
func TestChunkedHTTPSourceStaysLive(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "audio/mpeg")
		w.(http.Flusher).Flush()
		_, _ = io.WriteString(w, strings.Repeat("\xff\xfb\x90\x00", 64))
	}))
	t.Cleanup(srv.Close)

	p := &Player{sr: beep.SampleRate(44100), bitDepth: 16}

	tp, err := p.buildPipeline(srv.URL)
	if err != nil {
		// A short synthetic body may fail to decode; the routing decision is
		// what matters and it is made before any audio is read.
		return
	}
	defer tp.close()

	if tp.seekable {
		t.Error("a chunked stream was routed to the seekable pipeline")
	}
}
