package player

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

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

// TestLivePodcastRealLength measures a real episode and compares it with the
// feed's own itunes:duration. Set CLIAMP_LIVE_PODCAST=1, the URL, and the
// declared length in seconds to run it.
func TestLivePodcastRealLength(t *testing.T) {
	if os.Getenv("CLIAMP_LIVE_PODCAST") != "1" {
		t.Skip("set CLIAMP_LIVE_PODCAST=1 to run")
	}
	url := os.Getenv("CLIAMP_LIVE_PODCAST_URL")
	if url == "" {
		t.Skip("set CLIAMP_LIVE_PODCAST_URL to an episode enclosure URL")
	}

	p := &Player{sr: beep.SampleRate(44100), bitDepth: 16}
	p.RegisterBufferedURLMatcher(func(string) bool { return true })
	tp, err := p.buildPipeline(url)
	if err != nil {
		t.Fatalf("buildPipeline() error = %v", err)
	}
	defer tp.close()

	s, ok := tp.decoder.(*navFFmpegStreamer)
	if !ok {
		t.Fatalf("decoder type = %T, want *navFFmpegStreamer", tp.decoder)
	}
	s.probeDownloadedLength()

	frames := s.probed.Load()
	if frames <= 0 {
		t.Fatal("probe produced no length")
	}
	t.Logf("measured length: %v", p.sr.D(int(frames)).Round(time.Second))
}
