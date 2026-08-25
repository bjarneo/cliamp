package player

import (
	"net/http"
	"net/http/httptest"
	"testing"
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
