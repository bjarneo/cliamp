package player

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"testing"
	"time"

	"github.com/bjarneo/cliamp/internal/httpclient"
	"github.com/bjarneo/cliamp/playlist"
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
		{ext: ".mp4", want: true},
		{ext: ".webm", want: true},
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

func TestUsesLocalFFmpeg(t *testing.T) {
	tests := []struct {
		path string
		want bool
	}{
		{path: "/music/track.m4a", want: true},
		{path: "/music/track.mp4", want: true},
		{path: "/music/track.webm", want: true},
		{path: "/music/Track.OPUS", want: true},
		{path: "relative/track.wma", want: true},
		{path: "/music/track.mp3", want: false},
		{path: "/music/track.flac", want: false},
		{path: "https://example.com/track.m4a", want: false},
		{path: "ssh://host/music/track.m4a", want: false},
		{path: "spotify:track:abc", want: false},
	}
	for _, tt := range tests {
		if got := UsesLocalFFmpeg(tt.path); got != tt.want {
			t.Errorf("UsesLocalFFmpeg(%q) = %v, want %v", tt.path, got, tt.want)
		}
	}
}

// TestAudioExtensionsHaveADecoder checks that each extension that the
// playlist package accepts has a decoder route: a native decoder in
// decodeWithExt or the ffmpeg route in needsFFmpeg.
func TestAudioExtensionsHaveADecoder(t *testing.T) {
	native := []string{".mp3", ".wav", ".flac", ".ogg"}
	for _, ext := range playlist.AudioExtensions() {
		if !needsFFmpeg(ext) && !slices.Contains(native, ext) {
			t.Errorf("%s has no decoder route: add it to needsFFmpeg or decodeWithExt", ext)
		}
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
				if got := r.UserAgent(); got != httpclient.UserAgent {
					t.Errorf("User-Agent = %q, want %q", got, httpclient.UserAgent)
				}
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

func TestOpenSSHSourcePrefetchInterruptAndDoubleClose(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses POSIX shell fixture")
	}
	dir := t.TempDir()
	writeExecutable(t, filepath.Join(dir, "ssh"), "#!/bin/sh\nexec sleep 30\n")
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	src, err := openSSHSource("ssh://host/music/song.wav")
	if err != nil {
		t.Fatalf("openSSHSource() error = %v", err)
	}
	if !src.prefetch {
		t.Fatal("openSSHSource() prefetch = false, want true")
	}
	rc, ok := src.body.(*sshReadCloser)
	if !ok {
		_ = src.body.Close()
		t.Fatalf("body type = %T, want *sshReadCloser", src.body)
	}

	readDone := make(chan error, 1)
	go func() {
		_, err := rc.Read(make([]byte, 8))
		readDone <- err
	}()
	rc.interrupt()
	select {
	case <-readDone:
	case <-time.After(2 * time.Second):
		t.Fatal("interrupt() did not unblock Read")
	}

	if err := rc.Close(); err != nil {
		t.Fatalf("first Close() error = %v", err)
	}
	if err := rc.Close(); err != nil {
		t.Fatalf("second Close() error = %v", err)
	}
}

func TestFormatExt(t *testing.T) {
	tests := []struct {
		name string
		path string
		want string
	}{
		{name: "local mp3", path: "/music/song.mp3", want: ".mp3"},
		{name: "local flac upper", path: "/music/SONG.FLAC", want: ".flac"},
		{name: "url flac", path: "https://example.com/track.flac", want: ".flac"},
		{name: "url hls", path: "https://example.com/live/stream.m3u8", want: ".m3u8"},
		{name: "url subsonic view with format", path: "https://example.com/rest/stream.view?format=flac", want: ".flac"},
		{name: "url php falls back to mp3", path: "https://radio.example.com/stream.php?id=1", want: ".mp3"},
		{name: "url cgi with format query", path: "https://radio.example.com/listen.cgi?format=aac", want: ".aac"},
		{name: "url numeric bitrate suffix falls back to mp3", path: "https://radio.example.com/stream.128", want: ".mp3"},
		{name: "url ashx falls back to mp3", path: "https://radio.example.com/live.ashx", want: ".mp3"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := formatExt(tt.path); got != tt.want {
				t.Errorf("formatExt(%q) = %q, want %q", tt.path, got, tt.want)
			}
		})
	}
}
