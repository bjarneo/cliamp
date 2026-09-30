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
			"items": []map[string]any{
				{"item": map[string]any{
					"id": "t1", "name": "Song", "type": "track", "uri": "spotify:track:t1",
					"album": map[string]any{
						"name":   "Album",
						"images": []map[string]any{{"url": "https://i.scdn.co/300", "width": 300, "height": 300}},
					},
				}},
				{"item": map[string]any{
					"id": "e1", "name": "Episode", "type": "episode", "uri": "spotify:episode:e1",
					"images": []map[string]any{{"url": "https://i.scdn.co/episode", "width": 300}},
					"show":   map[string]any{"name": "Show", "images": []map[string]any{{"url": "https://i.scdn.co/show", "width": 300}}},
				}},
				{"item": map[string]any{
					"id": "e2", "name": "Episode 2", "type": "episode", "uri": "spotify:episode:e2",
					"show": map[string]any{"name": "Show", "images": []map[string]any{{"url": "https://i.scdn.co/show", "width": 300}}},
				}},
			},
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

	for _, group := range []string{"album", "show"} {
		m := regexp.MustCompile(group + `\(([^)]*)\)`).FindStringSubmatch(fields)
		if m == nil || !strings.Contains(m[1], "images") {
			t.Errorf("fields = %q, want %s(...) to request images", fields, group)
		}
	}
	// Episodes carry their own images at the item's top level.
	if !regexp.MustCompile(`item\([^()]*(\([^)]*\)[^()]*)*[,(]images[,)]`).MatchString(fields) {
		t.Errorf("fields = %q, want the item's own images", fields)
	}

	want := []string{"https://i.scdn.co/300", "https://i.scdn.co/episode", "https://i.scdn.co/show"}
	if len(tracks) != len(want) {
		t.Fatalf("got %d tracks, want %d", len(tracks), len(want))
	}
	for i, w := range want {
		if tracks[i].AlbumArtURL != w {
			t.Errorf("track %d (%s) art = %q, want %q", i, tracks[i].Title, tracks[i].AlbumArtURL, w)
		}
	}
}
