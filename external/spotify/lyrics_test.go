package spotify

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"golang.org/x/oauth2"

	"github.com/bjarneo/cliamp/lyrics"
)

func TestTrackIDFromPath(t *testing.T) {
	tests := []struct {
		name string
		path string
		want string
	}{
		{name: "track uri", path: "spotify:track:abc123", want: "abc123"},
		{name: "empty id", path: "spotify:track:", want: ""},
		{name: "episode uri", path: "spotify:episode:ep1", want: ""},
		{name: "https url", path: "https://open.spotify.com/track/abc", want: ""},
		{name: "local file", path: "/home/me/song.flac", want: ""},
		{name: "empty", path: "", want: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := TrackIDFromPath(tt.path); got != tt.want {
				t.Errorf("TrackIDFromPath(%q) = %q, want %q", tt.path, got, tt.want)
			}
		})
	}
}

func TestParseColorLyrics(t *testing.T) {
	tests := []struct {
		name    string
		data    string
		want    []lyrics.Line
		wantErr error
	}{
		{
			name: "line synced",
			data: `{"lyrics":{"syncType":"LINE_SYNCED","lines":[` +
				`{"startTimeMs":"12500","words":"first line"},` +
				`{"startTimeMs":"0","words":"intro"},` +
				`{"startTimeMs":"","words":""}]}}`,
			want: []lyrics.Line{
				{Start: 12500 * time.Millisecond, Text: "first line"},
				{Start: 0, Text: "intro"},
				{Start: 0, Text: ""},
			},
		},
		{
			name: "unsynced plain lines",
			data: `{"lyrics":{"syncType":"UNSYNCED","lines":[{"startTimeMs":"","words":"just words"}]}}`,
			want: []lyrics.Line{{Start: 0, Text: "just words"}},
		},
		{
			name:    "no lyrics key",
			data:    `{"colors":{}}`,
			wantErr: lyrics.ErrNotFound,
		},
		{
			name:    "empty lines",
			data:    `{"lyrics":{"syncType":"LINE_SYNCED","lines":[]}}`,
			wantErr: lyrics.ErrNotFound,
		},
		{
			name:    "invalid json",
			data:    `{not json`,
			wantErr: errors.New("decode"),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseColorLyrics([]byte(tt.data))
			if tt.wantErr != nil {
				if err == nil {
					t.Fatalf("parseColorLyrics() err = nil, want %v", tt.wantErr)
				}
				if !errors.Is(err, tt.wantErr) && !strings.Contains(err.Error(), tt.wantErr.Error()) {
					t.Fatalf("parseColorLyrics() err = %v, want %v", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseColorLyrics() unexpected err: %v", err)
			}
			if len(got) != len(tt.want) {
				t.Fatalf("got %d lines, want %d", len(got), len(tt.want))
			}
			for i, line := range got {
				if line != tt.want[i] {
					t.Errorf("line[%d] = %+v, want %+v", i, line, tt.want[i])
				}
			}
		})
	}
}

// lyricsSpotify fakes the color-lyrics endpoint and records each request.
func lyricsSpotify(t *testing.T, status int, body string) (*SpotifyProvider, *[]*http.Request) {
	t.Helper()
	var requests []*http.Request
	originalTransport := http.DefaultTransport
	http.DefaultTransport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		requests = append(requests, req)
		return &http.Response{
			StatusCode: status,
			Status:     fmt.Sprintf("%d %s", status, http.StatusText(status)),
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(body)),
			Request:    req,
		}, nil
	})
	t.Cleanup(func() { http.DefaultTransport = originalTransport })

	sess := &Session{tokenSource: oauth2.StaticTokenSource(&oauth2.Token{AccessToken: "token"})}
	return New(sess, "client", 320), &requests
}

func TestProviderTrackLyrics(t *testing.T) {
	body := `{"lyrics":{"syncType":"LINE_SYNCED","lines":[{"startTimeMs":"1000","words":"hello"}]}}`

	t.Run("fetches synced lyrics", func(t *testing.T) {
		prov, requests := lyricsSpotify(t, http.StatusOK, body)
		lines, err := prov.TrackLyrics(context.Background(), "trk1")
		if err != nil {
			t.Fatalf("TrackLyrics() err: %v", err)
		}
		if len(lines) != 1 || lines[0].Text != "hello" || lines[0].Start != time.Second {
			t.Fatalf("lines = %+v, want one synced line", lines)
		}
		if len(*requests) != 1 {
			t.Fatalf("got %d requests, want 1", len(*requests))
		}
		req := (*requests)[0]
		if req.URL.Path != "/color-lyrics/v2/track/trk1" {
			t.Errorf("path = %q, want /color-lyrics/v2/track/trk1", req.URL.Path)
		}
		if got := req.Header.Get("app-platform"); got != "WebPlayer" {
			t.Errorf("app-platform = %q, want WebPlayer", got)
		}
		if got := req.Header.Get("Authorization"); got != "Bearer token" {
			t.Errorf("Authorization = %q, want bearer token", got)
		}
	})

	t.Run("maps not found to ErrNotFound", func(t *testing.T) {
		prov, _ := lyricsSpotify(t, http.StatusNotFound, `{"error":{}}`)
		if _, err := prov.TrackLyrics(context.Background(), "trk1"); !errors.Is(err, lyrics.ErrNotFound) {
			t.Fatalf("err = %v, want ErrNotFound", err)
		}
	})

	t.Run("server error is wrapped", func(t *testing.T) {
		prov, _ := lyricsSpotify(t, http.StatusInternalServerError, `{}`)
		if _, err := prov.TrackLyrics(context.Background(), "trk1"); err == nil || errors.Is(err, lyrics.ErrNotFound) {
			t.Fatalf("err = %v, want non-NotFound error", err)
		}
	})

	t.Run("unsigned provider returns needs auth", func(t *testing.T) {
		prov := New(nil, "client", 320)
		_, err := prov.TrackLyrics(context.Background(), "trk1")
		if err == nil || !strings.Contains(err.Error(), "not signed in") {
			t.Fatalf("err = %v, want not-signed-in error", err)
		}
	})
}
