package spotify

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/bjarneo/cliamp/playlist"
)

// stubLibrary serves the Web API with status and records each request.
func stubLibrary(t *testing.T, status int, requests *[]*http.Request) *SpotifyProvider {
	t.Helper()
	rt := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		*requests = append(*requests, req)
		return &http.Response{
			StatusCode: status,
			Status:     http.StatusText(status),
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(`{"error":{"status":403,"message":"Insufficient client scope"}}`)),
			Request:    req,
		}, nil
	})
	sess := stubSession(rt)
	return New(sess, "client", 320)
}

func TestCanFavoriteTrack(t *testing.T) {
	p := New(nil, "client", 320)
	for _, tc := range []struct {
		path string
		want bool
	}{
		{path: "spotify:track:4uLU6hMCjMI75M1A2tKUQC", want: true},
		{path: "spotify:episode:512ojhOuo1ktJprKbVcKyQ"},
		{path: "spotify:album:4aawyAB9vmqN3uQ7FjRGTy"},
		{path: "/music/song.mp3"},
		{path: "https://open.spotify.com/track/4uLU6hMCjMI75M1A2tKUQC"},
	} {
		if got := p.CanFavoriteTrack(playlist.Track{Path: tc.path}); got != tc.want {
			t.Errorf("CanFavoriteTrack(%q) = %v, want %v", tc.path, got, tc.want)
		}
	}
}

func TestSetTrackFavorite(t *testing.T) {
	t.Parallel()
	const uri = "spotify:track:4uLU6hMCjMI75M1A2tKUQC"
	for _, tc := range []struct {
		name       string
		favorite   bool
		status     int
		wantMethod string
		wantErr    string
	}{
		{name: "save", favorite: true, status: http.StatusOK, wantMethod: http.MethodPut},
		{name: "remove", status: http.StatusOK, wantMethod: http.MethodDelete},
		{name: "missing scope", favorite: true, status: http.StatusForbidden, wantMethod: http.MethodPut, wantErr: "Insufficient client scope"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var requests []*http.Request
			p := stubLibrary(t, tc.status, &requests)
			p.trackCache[savedTracksPlaylistID] = &playlistCache{}
			p.listCache = []playlist.PlaylistInfo{{ID: savedTracksPlaylistID}}

			err := p.SetTrackFavorite(context.Background(), playlist.Track{Path: uri}, tc.favorite)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("err = %v, want %q", err, tc.wantErr)
				}
			} else if err != nil {
				t.Fatalf("SetTrackFavorite: %v", err)
			}
			if len(requests) != 1 {
				t.Fatalf("requests = %d, want 1", len(requests))
			}
			req := requests[0]
			if req.Method != tc.wantMethod || req.URL.Path != "/v1/me/library" || req.URL.Query().Get("uris") != uri {
				t.Fatalf("request = %s %s, want %s /v1/me/library?uris=%s", req.Method, req.URL, tc.wantMethod, uri)
			}
			if req.Body != nil && req.Body != http.NoBody {
				t.Fatal("library request sent a body")
			}
			_, cached := p.trackCache[savedTracksPlaylistID]
			if tc.wantErr == "" && (cached || p.listCache != nil) {
				t.Fatal("Liked Songs cache was not cleared")
			}
			if tc.wantErr != "" && !cached {
				t.Fatal("a failed call cleared the Liked Songs cache")
			}
		})
	}
}

func TestSetTrackFavoriteRejectsOtherTracks(t *testing.T) {
	t.Parallel()
	var requests []*http.Request
	p := stubLibrary(t, http.StatusOK, &requests)
	if err := p.SetTrackFavorite(context.Background(), playlist.Track{Path: "spotify:episode:1"}, true); err == nil {
		t.Fatal("an episode should be rejected")
	}
	if len(requests) != 0 {
		t.Fatalf("requests = %d, want 0", len(requests))
	}
}
