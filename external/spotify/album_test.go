//go:build !windows

package spotify

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"golang.org/x/oauth2"
)

// albumSpotify fakes the album endpoints plus a search that returns both albums
// and tracks. It records the search type parameter so the query itself is
// covered: asking for tracks only is exactly the bug this guards against.
func albumSpotify(t *testing.T, albumHits, trackHits, albumTracks int) (*SpotifyProvider, *string) {
	t.Helper()
	var searchType string

	originalTransport := http.DefaultTransport
	http.DefaultTransport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		var payload map[string]any

		switch path := req.URL.Path; {
		case path == "/v1/search":
			searchType = req.URL.Query().Get("type")
			albums := []map[string]any{}
			for i := range albumHits {
				albums = append(albums, map[string]any{
					"id":           fmt.Sprintf("al%d", i),
					"name":         fmt.Sprintf("Album %d", i),
					"type":         "album",
					"uri":          fmt.Sprintf("spotify:album:al%d", i),
					"release_date": "1994-07-19",
					"artists":      []map[string]any{{"name": "NOFX"}},
				})
			}
			tracks := []map[string]any{}
			for i := range trackHits {
				tracks = append(tracks, map[string]any{
					"id":   fmt.Sprintf("t%d", i),
					"name": fmt.Sprintf("Track %d", i),
					"type": "track",
					"uri":  fmt.Sprintf("spotify:track:t%d", i),
				})
			}
			payload = map[string]any{
				"albums":   map[string]any{"items": albums},
				"tracks":   map[string]any{"items": tracks},
				"episodes": map[string]any{"items": []any{}},
			}

		case strings.HasSuffix(path, "/tracks"):
			offset, _ := strconv.Atoi(req.URL.Query().Get("offset"))
			items := []map[string]any{}
			for i := offset; i < offset+spotifyTrackPageSize && i < albumTracks; i++ {
				items = append(items, map[string]any{
					"id":           fmt.Sprintf("at%d", i),
					"name":         fmt.Sprintf("Album track %d", i),
					"type":         "track",
					"uri":          fmt.Sprintf("spotify:track:at%d", i),
					"track_number": i + 1,
					"artists":      []map[string]any{{"name": "NOFX"}},
				})
			}
			payload = map[string]any{"items": items}

		case strings.HasPrefix(path, "/v1/albums/"):
			payload = map[string]any{
				"id":           "al0",
				"name":         "Punk In Drublic",
				"type":         "album",
				"uri":          "spotify:album:al0",
				"release_date": "1994-07-19",
				"artists":      []map[string]any{{"name": "NOFX"}},
			}

		default:
			return nil, fmt.Errorf("unexpected Spotify API path %q", path)
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
	return New(sess, "client", 320), &searchType
}

func TestSearchTracksLeadsWithAlbums(t *testing.T) {
	p, searchType := albumSpotify(t, 2, 3, 0)

	got, err := p.SearchTracks(context.Background(), "nofx", 10)
	if err != nil {
		t.Fatalf("SearchTracks() error = %v", err)
	}
	if *searchType != "album,track,episode" {
		t.Errorf("search type = %q, want %q", *searchType, "album,track,episode")
	}
	if len(got) != 5 {
		t.Fatalf("got %d results, want 5", len(got))
	}
	for i, want := range []bool{true, true, false, false, false} {
		if got[i].IsAlbum() != want {
			t.Errorf("result %d IsAlbum() = %v, want %v", i, got[i].IsAlbum(), want)
		}
	}
	if id := got[0].AlbumID(); id != "al0" {
		t.Errorf("album id = %q, want %q", id, "al0")
	}
	if got[0].Year != 1994 {
		t.Errorf("album year = %d, want 1994", got[0].Year)
	}
	if got[2].AlbumID() != "" {
		t.Errorf("track reported album id %q, want none", got[2].AlbumID())
	}
}

func TestAlbumTracksPagesAndFillsAlbumMetadata(t *testing.T) {
	const total = spotifyTrackPageSize + 3
	p, _ := albumSpotify(t, 0, 0, total)

	got, err := p.AlbumTracks("al0")
	if err != nil {
		t.Fatalf("AlbumTracks() error = %v", err)
	}
	if len(got) != total {
		t.Fatalf("got %d tracks, want %d", len(got), total)
	}
	for i, tr := range got {
		// /v1/albums/{id}/tracks omits the album, so every track must inherit it.
		if tr.Album != "Punk In Drublic" {
			t.Fatalf("track %d album = %q, want %q", i, tr.Album, "Punk In Drublic")
		}
		if tr.Year != 1994 {
			t.Fatalf("track %d year = %d, want 1994", i, tr.Year)
		}
		if tr.IsAlbum() {
			t.Fatalf("track %d is marked as an album placeholder", i)
		}
	}
	if got[0].Path != "spotify:track:at0" {
		t.Errorf("first track path = %q, want %q", got[0].Path, "spotify:track:at0")
	}
}
