package resolve

import (
	"context"
	"io"
	"net/http"
	"slices"
	"testing"

	"github.com/bjarneo/cliamp/internal/httpclient/httpclienttest"
)

// TestClientsUseEnvironmentProxy checks that feed, sniff and YouTube page
// requests follow ALL_PROXY. The test proxy refuses https tunnels, so the
// YouTube lookup fails after it reaches the proxy.
func TestClientsUseEnvironmentProxy(t *testing.T) {
	tests := []struct {
		name     string
		call     func(t *testing.T)
		wantHost string
	}{
		{
			name: "feed",
			call: func(t *testing.T) {
				if _, err := Feed(context.Background(), "http://feeds.example.invalid/show.rss"); err != nil {
					t.Fatalf("Feed: %v", err)
				}
			},
			wantHost: "feeds.example.invalid",
		},
		{
			name: "sniff",
			call: func(t *testing.T) {
				if !sniffFeedURL(context.Background(), "http://radio.example.invalid/live") {
					t.Fatal("sniffFeedURL = false, want true for an RSS content type")
				}
			},
			wantHost: "radio.example.invalid",
		},
		{
			name: "youtube page",
			call: func(t *testing.T) {
				if _, err := resolveYouTube(context.Background(), "https://www.youtube.com/watch?v=dQw4w9WgXcQ"); err == nil {
					t.Fatal("resolveYouTube succeeded through a proxy that opens no tunnels")
				}
			},
			wantHost: "www.youtube.com:443",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			proxy := httpclienttest.UseAllProxy(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/rss+xml")
				_, _ = io.WriteString(w, `<rss><channel><title>Show</title></channel></rss>`)
			}))
			tt.call(t)
			// The YouTube library can retry a failed request, so compare
			// the distinct hosts.
			if got := slices.Compact(proxy.Hosts()); !slices.Equal(got, []string{tt.wantHost}) {
				t.Errorf("proxy hosts = %q, want [%s]", got, tt.wantHost)
			}
		})
	}
}
