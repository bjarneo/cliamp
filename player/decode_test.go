package player

import (
	"net/http"
	"net/http/httptest"
	"os/exec"
	"testing"

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
