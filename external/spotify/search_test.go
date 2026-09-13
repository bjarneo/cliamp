//go:build !windows

package spotify

import (
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/bjarneo/cliamp/provider"
)

var searchAllJSON = `{"tracks":{"items":[` +
	trackJSON("t1") +
	`]},` +
	`"episodes":{"items":[` +
	`{"id":"e1","name":"Episode 42","type":"episode","uri":"spotify:episode:e1","duration_ms":3600000}` +
	`]},` +
	`"albums":{"items":[` +
	`{"id":"al1","name":"Discovery","release_date":"2001-03-12","total_tracks":14,` +
	`"artists":[{"id":"art1","name":"Daft Punk"}]}` +
	`]},` +
	`"artists":{"items":[` +
	`{"id":"art1","name":"Daft Punk"}` +
	`]},` +
	`"playlists":{"items":[` +
	`{"id":"pl1","name":"Mine","owner":{"id":"me"},"tracks":{"total":10}},` +
	`{"id":"pl2","name":"Theirs","owner":{"id":"other"},"tracks":{"total":20}},` +
	`{"id":"pl3","name":"NewStyle","owner":{"id":"other"},"items":{"total":5}}` +
	`]}}`

func TestSearchAllMapsAllTypes(t *testing.T) {
	m := newMockAPI(t)
	m.handlers["/v1/me"] = func(t *testing.T, query url.Values) string {
		return `{"id":"me"}`
	}
	m.handlers["/v1/search"] = func(t *testing.T, query url.Values) string {
		if got := query.Get("q"); got != "daft punk" {
			t.Errorf("q = %q, want daft punk", got)
		}
		if got := query.Get("type"); got != "track,album,artist,playlist,episode" {
			t.Errorf("type = %q, want all five types", got)
		}
		return searchAllJSON
	}

	got, err := newTestProvider().SearchAll(t.Context(), "daft punk", 10)
	if err != nil {
		t.Fatal(err)
	}

	// Tracks: music tracks first, episodes merged after with their URI kept.
	if len(got.Tracks) != 2 {
		t.Fatalf("Tracks = %d entries, want 2 (track + episode)", len(got.Tracks))
	}
	if got.Tracks[0].Path != "spotify:track:t1" {
		t.Errorf("track Path = %q, want spotify:track:t1", got.Tracks[0].Path)
	}
	if got.Tracks[1].Path != "spotify:episode:e1" {
		t.Errorf("episode Path = %q, want spotify:episode:e1 (URI preserved)", got.Tracks[1].Path)
	}
	if got.Tracks[1].Title != "Episode 42" {
		t.Errorf("episode Title = %q, want Episode 42", got.Tracks[1].Title)
	}

	wantAlbums := []provider.AlbumInfo{
		{ID: "al1", Name: "Discovery", Artist: "Daft Punk", ArtistID: "art1", Year: 2001, TrackCount: 14},
	}
	if len(got.Albums) != len(wantAlbums) {
		t.Fatalf("Albums = %+v, want %+v", got.Albums, wantAlbums)
	}
	if got.Albums[0] != wantAlbums[0] {
		t.Errorf("album = %+v, want %+v", got.Albums[0], wantAlbums[0])
	}

	wantArtists := []provider.ArtistInfo{{ID: "art1", Name: "Daft Punk"}}
	if len(got.Artists) != len(wantArtists) {
		t.Fatalf("Artists = %+v, want %+v", got.Artists, wantArtists)
	}
	if got.Artists[0] != wantArtists[0] {
		t.Errorf("artist = %+v, want %+v", got.Artists[0], wantArtists[0])
	}

	// Playlists: Owned on owner == current user, Section left empty, and the
	// track total read from either "tracks" or "items".
	wantPlaylists := []struct {
		id      string
		count   int
		owned   bool
		section string
	}{
		{id: "pl1", count: 10, owned: true},
		{id: "pl2", count: 20, owned: false},
		{id: "pl3", count: 5, owned: false},
	}
	if len(got.Playlists) != len(wantPlaylists) {
		t.Fatalf("Playlists = %+v, want %d entries", got.Playlists, len(wantPlaylists))
	}
	for i, want := range wantPlaylists {
		got := got.Playlists[i]
		if got.ID != want.id || got.TrackCount != want.count || got.Owned != want.owned || got.Section != want.section {
			t.Errorf("playlist %d = {ID:%s Count:%d Owned:%v Section:%q}, want %+v",
				i, got.ID, got.TrackCount, got.Owned, got.Section, want)
		}
	}
}

func TestSearchAllLimitClamp(t *testing.T) {
	tests := []struct {
		name  string
		limit int
		want  string
	}{
		{name: "zero clamps to one", limit: 0, want: "1"},
		{name: "negative clamps to one", limit: -3, want: "1"},
		{name: "one stays one", limit: 1, want: "1"},
		{name: "ten stays ten", limit: 10, want: "10"},
		{name: "over ten clamps to ten", limit: 50, want: "10"},
		{name: "way over ten clamps to ten", limit: 100, want: "10"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := newMockAPI(t)
			m.handlers["/v1/search"] = func(t *testing.T, query url.Values) string {
				return `{}`
			}
			if _, err := newTestProvider().SearchAll(t.Context(), "q", tt.limit); err != nil {
				t.Fatal(err)
			}
			if got := m.last["/v1/search"].Get("limit"); got != tt.want {
				t.Errorf("limit = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestSearchAllDevModeBlocked(t *testing.T) {
	original := http.DefaultTransport
	http.DefaultTransport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusBadRequest,
			Status:     "400 Bad Request",
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(`{"error":{"message":"Invalid limit"}}`)),
			Request:    req,
		}, nil
	})
	t.Cleanup(func() { http.DefaultTransport = original })

	_, err := newTestProvider().SearchAll(t.Context(), "x", 10)
	if err == nil || !strings.Contains(err.Error(), "search blocked") {
		t.Fatalf("err = %v, want the dev-mode search-blocked rewrite", err)
	}
}
