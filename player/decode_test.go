package player

import (
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gopxl/beep/v2"
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

func TestIsLowRateMP3(t *testing.T) {
	tests := []struct {
		ext  string
		sr   beep.SampleRate
		want bool
	}{
		{ext: ".mp3", sr: 22050, want: true},  // MPEG-2
		{ext: ".mp3", sr: 24000, want: true},  // MPEG-2
		{ext: ".mp3", sr: 16000, want: true},  // MPEG-2
		{ext: ".mp3", sr: 11025, want: true},  // MPEG-2.5
		{ext: ".mp3", sr: 44100, want: false}, // MPEG-1
		{ext: ".mp3", sr: 48000, want: false},
		{ext: ".mp3", sr: 32000, want: false}, // MPEG-1 floor, not low-rate
		{ext: ".mp3", sr: 0, want: false},     // guard against unset format
		{ext: ".ogg", sr: 22050, want: false}, // only applies to mp3
	}
	for _, tt := range tests {
		if got := isLowRateMP3(tt.ext, tt.sr); got != tt.want {
			t.Errorf("isLowRateMP3(%q, %d) = %v, want %v", tt.ext, tt.sr, got, tt.want)
		}
	}
}

// TestBuildPipelineRoutesLowRateMP3ToFFmpeg is a regression test for a real
// bug: go-mp3 (the native decoder beep's mp3 package wraps) parses MPEG-2/2.5
// Layer III streams (sample rates below 32000Hz) without error but produces
// audibly garbled/distorted PCM. This was confirmed by ear against a real
// 22050Hz Icecast radio stream (RTHK Radio 2, stm2.rthk.hk/radio2) — decoding
// the identical captured bytes with ffmpeg sounded correct, decoding with
// go-mp3 did not. buildPipeline must route this class of stream to ffmpeg
// instead of trusting go-mp3's error-free-but-wrong output.
func TestBuildPipelineRoutesLowRateMP3ToFFmpeg(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not installed")
	}

	p := &Player{sr: 44100, bitDepth: 16, resampleQuality: 4}
	tp, err := p.buildPipeline("testdata/mpeg2_22050.mp3")
	if err != nil {
		t.Fatalf("buildPipeline: %v", err)
	}
	defer tp.close()

	if _, ok := tp.decoder.(*localFFmpegStreamer); !ok {
		t.Fatalf("decoder = %T, want *localFFmpegStreamer (go-mp3 should have been bypassed)", tp.decoder)
	}

	// Sanity: the ffmpeg fallback actually produces audio.
	buf := make([][2]float64, 512)
	n, _ := tp.stream.Stream(buf)
	if n == 0 {
		t.Fatal("ffmpeg fallback decoder produced no samples")
	}
}

// TestBuildPipelineLowRateMP3HTTPPreservesICYMetadata is a regression test
// for a review finding on this fix: the low-rate MP3 fallback must reopen
// HTTP sources through the normal ICY-aware reader chain
// (decodeFFmpegPipeStream, fed via stdin) rather than letting ffmpeg fetch
// the URL itself (decodeFFmpegURLStream) — the latter bypasses openSource's
// ICY reader entirely and silently drops StreamTitle updates, which matters
// specifically for the low-bitrate HTTP radio streams this fix targets.
func TestBuildPipelineLowRateMP3HTTPPreservesICYMetadata(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not installed")
	}

	audio, err := os.ReadFile("testdata/mpeg2_22050.mp3")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}

	const metaInt = 1500
	if len(audio) <= metaInt {
		t.Fatalf("fixture too small (%d bytes) for metaInt=%d", len(audio), metaInt)
	}

	const wantTitle = "Test Song - Regression"
	meta := "StreamTitle='" + wantTitle + "';"
	// ICY declares the metadata block's length in 16-byte units; pad to fill it.
	metaBlockLen := (len(meta)/16 + 1) * 16
	metaBlock := make([]byte, metaBlockLen)
	copy(metaBlock, meta)

	// Only the reopened (second) request carries ICY metadata. The first
	// request is the discarded go-mp3 probe; if it carried metadata, the title
	// could arrive from that connection and mask a fallback that loses ICY.
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "audio/mpeg")
		w.Header().Set("Icy-Name", "Test Radio")
		if requests.Add(1) < 2 {
			w.WriteHeader(http.StatusOK)
			w.Write(audio)
			return
		}
		w.Header().Set("Icy-Metaint", strconv.Itoa(metaInt))
		w.WriteHeader(http.StatusOK)

		w.Write(audio[:metaInt])
		w.Write([]byte{byte(metaBlockLen / 16)})
		w.Write(metaBlock)
		w.Write(audio[metaInt:])
	}))
	defer server.Close()

	p := &Player{sr: 44100, bitDepth: 16, resampleQuality: 4}
	tp, err := p.buildPipeline(server.URL)
	if err != nil {
		t.Fatalf("buildPipeline: %v", err)
	}
	defer tp.close()

	if _, ok := tp.decoder.(*ffmpegPipeStreamer); !ok {
		t.Fatalf("decoder = %T, want *ffmpegPipeStreamer (should feed ffmpeg via stdin to keep the ICY reader attached)", tp.decoder)
	}

	// Pull samples until the stdin-copy goroutine has fed ffmpeg past the
	// metadata block and the ICY callback has fired, or time out.
	deadline := time.Now().Add(5 * time.Second)
	buf := make([][2]float64, 512)
	for p.StreamTitle() == "" && time.Now().Before(deadline) {
		tp.stream.Stream(buf)
	}

	if got := p.StreamTitle(); got != wantTitle {
		t.Fatalf("StreamTitle() = %q, want %q (ICY metadata was lost)", got, wantTitle)
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

			src, err := openSource(server.URL, nil)
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
