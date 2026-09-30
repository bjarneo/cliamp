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

	"github.com/bjarneo/cliamp/playlist"
)

// stubSpotifyAPI serves each request path from routes and fails on others.
func stubSpotifyAPI(t *testing.T, clientID string, routes map[string]func(*http.Request) *http.Response) *SpotifyProvider {
	t.Helper()
	originalTransport := http.DefaultTransport
	http.DefaultTransport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		route, ok := routes[req.URL.Path]
		if !ok {
			return nil, fmt.Errorf("unexpected Spotify API path %q", req.URL.Path)
		}
		resp := route(req)
		resp.Request = req
		if resp.Header == nil {
			resp.Header = make(http.Header)
		}
		return resp, nil
	})
	t.Cleanup(func() { http.DefaultTransport = originalTransport })
	sess := &Session{tokenSource: oauth2.StaticTokenSource(&oauth2.Token{AccessToken: "token"})}
	return New(sess, clientID, 320)
}

func apiResponse(status int, body string) func(*http.Request) *http.Response {
	return func(*http.Request) *http.Response {
		return &http.Response{
			StatusCode: status,
			Status:     fmt.Sprintf("%d %s", status, http.StatusText(status)),
			Body:       io.NopCloser(strings.NewReader(body)),
		}
	}
}

func TestNewAPIErrorReadsSpotifyMessage(t *testing.T) {
	tests := []struct {
		name string
		body string
		want string
	}{
		{name: "web api error", body: `{"error":{"status":403,"message":"Forbidden"}}`, want: "http status 403: Forbidden"},
		{name: "oauth error", body: `{"error":"invalid_client","error_description":"Invalid client"}`, want: "http status 403: Invalid client"},
		{name: "plain string error", body: `{"error":"quota"}`, want: "http status 403: quota"},
		{name: "not json", body: "upstream down", want: "http status 403: upstream down"},
		{name: "empty body", body: "", want: "http status 403: Forbidden"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := newAPIError(http.StatusForbidden, []byte(tt.body)).Error(); got != tt.want {
				t.Fatalf("Error() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestWebAPIReportsLongRetryAfterWithoutWaiting(t *testing.T) {
	tests := []struct {
		name     string
		clientID string
		wantHint string
	}{
		{name: "built-in client", clientID: DefaultClientID, wantHint: "set client_id in [spotify]"},
		{name: "own client", clientID: "own", wantHint: "try again later"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := stubSpotifyAPI(t, tt.clientID, map[string]func(*http.Request) *http.Response{
				"/v1/me": func(*http.Request) *http.Response {
					resp := apiResponse(http.StatusTooManyRequests, "")(nil)
					resp.Header = http.Header{"Retry-After": {"86400"}}
					return resp
				},
			})

			start := time.Now()
			_, err := p.webAPI(context.Background(), "GET", "/v1/me", nil)
			if err == nil {
				t.Fatal("webAPI() error = nil, want rate-limit error")
			}
			if elapsed := time.Since(start); elapsed > time.Second {
				t.Fatalf("webAPI() waited %v, want an immediate error", elapsed)
			}
			for _, want := range []string{"rate-limited on /v1/me", "24h0m0s", tt.wantHint} {
				if !strings.Contains(err.Error(), want) {
					t.Fatalf("error = %q, want it to contain %q", err, want)
				}
			}
			if strings.Contains(err.Error(), "re-authenticat") {
				t.Fatalf("error = %q, want no advice to sign in again", err)
			}
		})
	}
}

func TestTracksExplainsForbiddenPlaylist(t *testing.T) {
	p := stubSpotifyAPI(t, "own", map[string]func(*http.Request) *http.Response{
		"/v1/playlists/other/items": apiResponse(http.StatusForbidden, `{"error":{"status":403,"message":"Forbidden"}}`),
	})

	_, err := p.Tracks("other")
	if err == nil {
		t.Fatal("Tracks() error = nil, want 403")
	}
	if !strings.Contains(err.Error(), "playlists you own or collaborate on") || !hasStatus(err, http.StatusForbidden) {
		t.Fatalf("error = %q, want an explanation that wraps the 403", err)
	}
}

func playlistRoutes(albums func(*http.Request) *http.Response) map[string]func(*http.Request) *http.Response {
	return map[string]func(*http.Request) *http.Response{
		"/v1/me":        apiResponse(http.StatusOK, `{"id":"me"}`),
		"/v1/me/tracks": apiResponse(http.StatusOK, `{"total":3}`),
		"/v1/me/playlists": apiResponse(http.StatusOK, `{"total":3,"items":[
			{"id":"mine","name":"Mine","owner":{"id":"me"}},
			{"id":"shared","name":"Shared","collaborative":true,"owner":{"id":"friend"}},
			{"id":"followed","name":"Followed","owner":{"id":"friend"}}
		]}`),
		"/v1/me/albums": albums,
	}
}

func TestPlaylistsKeepsPlaylistsWhenSavedAlbumsFail(t *testing.T) {
	calls := 0
	p := stubSpotifyAPI(t, "own", playlistRoutes(func(req *http.Request) *http.Response {
		calls++
		return apiResponse(http.StatusInternalServerError, `{"error":{"status":500,"message":"Server error"}}`)(req)
	}))

	lists, err := p.Playlists()
	if err == nil {
		t.Fatal("Playlists() error = nil, want the saved-albums error")
	}
	if len(lists) != 4 {
		t.Fatalf("Playlists() returned %d entries, want Your Music and 3 playlists", len(lists))
	}
	if _, err := p.Playlists(); err == nil || calls != 2 {
		t.Fatalf("second Playlists(): err = %v, album calls = %d, want no cached partial list", err, calls)
	}
}

func TestCanAddToPlaylistAcceptsOwnedAndCollaborative(t *testing.T) {
	p := stubSpotifyAPI(t, "own", playlistRoutes(apiResponse(http.StatusOK, `{"total":1,"items":[
		{"album":{"id":"a1","name":"Album","total_tracks":3}}
	]}`)))

	lists, err := p.Playlists()
	if err != nil {
		t.Fatalf("Playlists() error = %v", err)
	}
	want := map[string]bool{
		savedTracksPlaylistID:     false,
		"mine":                    true,
		"shared":                  true,
		"followed":                false,
		savedAlbumIDPrefix + "a1": false,
	}
	if len(lists) != len(want) {
		t.Fatalf("Playlists() returned %d entries, want %d", len(lists), len(want))
	}
	for _, pl := range lists {
		if got := p.CanAddToPlaylist(pl); got != want[pl.ID] {
			t.Errorf("CanAddToPlaylist(%q) = %v, want %v", pl.ID, got, want[pl.ID])
		}
	}
}

func TestRefreshDropsCachedLists(t *testing.T) {
	p := New(nil, "own", 320)
	p.listCache = []playlist.PlaylistInfo{{ID: "stale"}}
	p.listCacheAt = time.Now()
	p.trackCache["mine"] = &playlistCache{snapshotID: "s1", tracks: []playlist.Track{{Path: "spotify:track:1"}}}
	p.writable["mine"] = true

	p.Refresh()

	if p.listCache != nil || len(p.trackCache) != 0 || len(p.writable) != 0 {
		t.Fatalf("after Refresh: listCache = %v, trackCache = %v, writable = %v, want all empty", p.listCache, p.trackCache, p.writable)
	}
}

func TestSessionResetDropsWritablePlaylists(t *testing.T) {
	p := New(nil, "own", 320)
	p.listCache = []playlist.PlaylistInfo{{ID: "mine"}}
	p.writable["mine"] = true

	p.mu.Lock()
	p.resetSessionScopedStateLocked()
	p.mu.Unlock()

	if p.listCache != nil || p.CanAddToPlaylist(playlist.PlaylistInfo{ID: "mine"}) {
		t.Fatalf("after reset: listCache = %v, writable = %v, want both empty", p.listCache, p.writable)
	}
}

func TestIsInvalidLimitNeedsBadRequest(t *testing.T) {
	if !isInvalidLimit(fmt.Errorf("page: %w", newAPIError(http.StatusBadRequest, []byte(`{"error":{"message":"Invalid limit"}}`)))) {
		t.Fatal("isInvalidLimit() = false for 400 Invalid limit")
	}
	if isInvalidLimit(newAPIError(http.StatusForbidden, []byte(`{"error":{"message":"Invalid limit"}}`))) {
		t.Fatal("isInvalidLimit() = true for a 403")
	}
	if isInvalidLimit(errors.New("400 Invalid limit")) {
		t.Fatal("isInvalidLimit() = true for a plain error")
	}
}
