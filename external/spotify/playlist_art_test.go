//go:build !windows

package spotify

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"regexp"
	"strings"
	"testing"

	"golang.org/x/oauth2"
)

// Playlist items are fetched with a fields filter, and Spotify drops anything
// the filter does not name. The cover has to be asked for explicitly, or every
// playlist track arrives without art even though saved tracks have it.
func TestPlaylistTracksRequestAlbumArt(t *testing.T) {
	var fields string

	originalTransport := http.DefaultTransport
	http.DefaultTransport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		fields = req.URL.Query().Get("fields")
		payload := map[string]any{
			"total": 1,
			"items": []map[string]any{{
				"item": map[string]any{
					"id": "t1", "name": "Song", "type": "track", "uri": "spotify:track:t1",
					"album": map[string]any{
						"name":   "Album",
						"images": []map[string]any{{"url": "https://i.scdn.co/300", "width": 300, "height": 300}},
					},
				},
			}},
		}
		body, _ := json.Marshal(payload)
		return &http.Response{
			StatusCode: http.StatusOK,
			Status:     "200 OK",
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(string(body))),
			Request:    req,
		}, nil
	})
	t.Cleanup(func() { http.DefaultTransport = originalTransport })

	sess := &Session{tokenSource: oauth2.StaticTokenSource(&oauth2.Token{AccessToken: "token"})}
	p := New(sess, "client", 320)

	tracks, _, err := p.fetchTracksPage(context.Background(), "playlist-id", 0)
	if err != nil {
		t.Fatalf("fetchTracksPage: %v", err)
	}

	album := regexp.MustCompile(`album\(([^)]*)\)`).FindStringSubmatch(fields)
	if album == nil || !strings.Contains(album[1], "images") {
		t.Errorf("fields = %q, want album(...) to request images", fields)
	}
	if len(tracks) != 1 || tracks[0].AlbumArtURL != "https://i.scdn.co/300" {
		t.Errorf("tracks = %+v, want one track with the album art", tracks)
	}
}
