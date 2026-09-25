package spotify

import (
	"fmt"
	"net/url"
	"slices"
	"strings"
	"testing"

	"github.com/bjarneo/cliamp/playlist"
)

// topArtistsPage builds a /v1/me/top/artists page naming artists "Artist <id>".
func topArtistsPage(ids ...string) string {
	return fmt.Sprintf(`{"items":[%s],"total":%d}`, artistItems(ids...), len(ids))
}

// topTracksPage builds a /v1/me/top/tracks page from track ids.
func topTracksPage(ids ...string) string {
	parts := make([]string, len(ids))
	for i, id := range ids {
		parts[i] = trackJSON(id)
	}
	return fmt.Sprintf(`{"items":[%s],"total":%d}`, strings.Join(parts, ","), len(ids))
}

// albumJSON builds a SimplifiedAlbum object for artists/{id}/albums pages.
func albumJSON(id, releaseDate string) string {
	return fmt.Sprintf(`{"id":%q,"name":"Album %s","release_date":%q,"total_tracks":2,`+
		`"artists":[{"id":"a1","name":"Artist a1"}],"images":[{"url":"https://img/%s"}]}`,
		id, id, releaseDate, id)
}

// albumTracksPage builds a /v1/albums/{id}/tracks page of simplified tracks.
func albumTracksPage(ids ...string) string {
	parts := make([]string, len(ids))
	for i, id := range ids {
		parts[i] = trackJSON(id)
	}
	return fmt.Sprintf(`{"items":[%s],"total":%d}`, strings.Join(parts, ","), len(ids))
}

func TestRecommendTracksFamiliarOnly(t *testing.T) {
	m := newMockAPI(t)
	m.handlers["/v1/me/top/tracks"] = func(t *testing.T, query url.Values) string {
		if got := query.Get("time_range"); got != "medium_term" {
			t.Errorf("time_range = %q, want medium_term", got)
		}
		if got := query.Get("limit"); got != "50" {
			t.Errorf("limit = %q, want 50 (the endpoint max)", got)
		}
		return topTracksPage("t1", "t2", "t3")
	}
	// No /v1/me/top/artists handler: familiar fills the limit, so discovery
	// must never run.

	got, err := newTestProvider().RecommendTracks(t.Context(), nil, 2)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"spotify:track:t1", "spotify:track:t2"}
	if paths := trackPaths(got); !slices.Equal(paths, want) {
		t.Fatalf("paths = %v, want %v", paths, want)
	}
	if n := m.calls("/v1/me/top/artists"); n != 0 {
		t.Errorf("made %d top-artists calls, want 0 (limit already reached)", n)
	}
}

func TestRecommendTracksDedupesAgainstSeedAndWithin(t *testing.T) {
	m := newMockAPI(t)
	m.handlers["/v1/me/top/tracks"] = func(t *testing.T, query url.Values) string {
		return topTracksPage("tSeed", "t1", "t1")
	}
	m.handlers["/v1/me/top/artists"] = func(t *testing.T, query url.Values) string {
		return topArtistsPage("a1")
	}
	m.handlers["/v1/artists/a1/albums"] = func(t *testing.T, query url.Values) string {
		return fmt.Sprintf(`{"items":[%s],"total":1}`, albumJSON("al1", "2020-01-01"))
	}
	m.handlers["/v1/albums/al1/tracks"] = func(t *testing.T, query url.Values) string {
		return albumTracksPage("t1", "t2")
	}

	seed := []playlist.Track{
		{Path: "spotify:track:tSeed"},
		{ProviderMeta: map[string]string{metaSpotifyID: "tMeta"}}, // seed via ProviderMeta id
	}
	got, err := newTestProvider().RecommendTracks(t.Context(), seed, 10)
	if err != nil {
		t.Fatal(err)
	}
	paths := trackPaths(got)
	if len(paths) != 2 || paths[0] != "spotify:track:t1" || paths[1] != "spotify:track:t2" {
		t.Fatalf("paths = %v, want [spotify:track:t1 spotify:track:t2] (seed and dup removed)", paths)
	}
}

func TestRecommendTracksDiscoveryChain(t *testing.T) {
	m := newMockAPI(t)
	m.handlers["/v1/me/top/tracks"] = func(t *testing.T, query url.Values) string {
		return topTracksPage("tFam")
	}
	m.handlers["/v1/me/top/artists"] = func(t *testing.T, query url.Values) string {
		if got := query.Get("limit"); got != "20" {
			t.Errorf("limit = %q, want 20", got)
		}
		return topArtistsPage("aSeed", "a2")
	}
	m.handlers["/v1/artists/a2/albums"] = func(t *testing.T, query url.Values) string {
		if got := query.Get("include_groups"); got != "album" {
			t.Errorf("include_groups = %q, want album", got)
		}
		if got := query.Get("limit"); got != "10" {
			t.Errorf("limit = %q, want 10 (the per-request max)", got)
		}
		// Deliberately oldest-first: the impl must sort newest-first itself.
		return fmt.Sprintf(`{"items":[%s,%s],"total":2}`,
			albumJSON("alOld", "2001-01-01"), albumJSON("alNew", "2020-06-01"))
	}
	m.handlers["/v1/albums/alNew/tracks"] = func(t *testing.T, query url.Values) string {
		if got := query.Get("limit"); got != "50" {
			t.Errorf("limit = %q, want 50", got)
		}
		return albumTracksPage("tN1", "tN2")
	}
	m.handlers["/v1/albums/alOld/tracks"] = func(t *testing.T, query url.Values) string {
		return albumTracksPage("tO1")
	}

	seed := []playlist.Track{
		{Path: "spotify:track:tFam"},
		{ProviderMeta: map[string]string{metaSpotifyArtistIDs: "aSeed"}}, // aSeed is not absent: filtered out
	}
	got, err := newTestProvider().RecommendTracks(t.Context(), seed, 10)
	if err != nil {
		t.Fatal(err)
	}

	// Familiar first (filtered against seed), then the newest album's tracks
	// before the older one's.
	paths := trackPaths(got)
	want := []string{"spotify:track:tN1", "spotify:track:tN2", "spotify:track:tO1"}
	if len(paths) != 3 {
		t.Fatalf("paths = %v, want %v", paths, want)
	}
	for i := range want {
		if paths[i] != want[i] {
			t.Fatalf("paths = %v, want %v (newest album first)", paths, want)
		}
	}

	// Album metadata is merged into the discovery tracks.
	if got[0].Album != "Album alNew" || got[0].Year != 2020 || got[0].AlbumArtURL != "https://img/alNew" {
		t.Errorf("track[0] = {Album:%q Year:%d Art:%q}, want {Album alNew 2020 https://img/alNew}",
			got[0].Album, got[0].Year, got[0].AlbumArtURL)
	}
	if id := got[0].ProviderMeta[metaSpotifyID]; id != "tN1" {
		t.Errorf("ProviderMeta[%q] = %q, want tN1", metaSpotifyID, id)
	}

	// The seed artist's albums were never requested.
	if n := m.calls("/v1/artists/aSeed/albums"); n != 0 {
		t.Errorf("made %d calls for seed artist, want 0", n)
	}
}

func TestRecommendTracksDiscoveryCapsAtThreeArtists(t *testing.T) {
	m := newMockAPI(t)
	m.handlers["/v1/me/top/tracks"] = func(t *testing.T, query url.Values) string {
		return `{"items":[],"total":0}`
	}
	m.handlers["/v1/me/top/artists"] = func(t *testing.T, query url.Values) string {
		return topArtistsPage("a1", "a2", "a3", "a4", "a5")
	}
	for _, id := range []string{"a1", "a2", "a3", "a4"} {
		m.handlers["/v1/artists/"+id+"/albums"] = func(t *testing.T, query url.Values) string {
			return `{"items":[],"total":0}`
		}
	}

	got, err := newTestProvider().RecommendTracks(t.Context(), nil, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("got %d tracks, want 0 (empty albums)", len(got))
	}
	for _, id := range []string{"a1", "a2", "a3"} {
		if n := m.calls("/v1/artists/" + id + "/albums"); n != 1 {
			t.Errorf("made %d calls for %s, want 1", n, id)
		}
	}
	if n := m.calls("/v1/artists/a4/albums"); n != 0 {
		t.Errorf("made %d calls for a4, want 0 (capped at 3 discovery artists)", n)
	}
}

func TestRecommendTracksPartialFailureKeepsResults(t *testing.T) {
	m := newMockAPI(t)
	m.handlers["/v1/me/top/tracks"] = func(t *testing.T, query url.Values) string {
		return topTracksPage("t1", "t2")
	}
	m.handlers["/v1/me/top/artists"] = func(t *testing.T, query url.Values) string {
		return topArtistsPage("a1")
	}
	// No /v1/artists/a1/albums handler: the album call fails (transport
	// error) and the chain must be skipped, not fatal.

	got, err := newTestProvider().RecommendTracks(t.Context(), nil, 10)
	if err != nil {
		t.Fatalf("err = %v, want nil (partial failure is never fatal)", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d tracks, want 2 familiar results", len(got))
	}
}

func TestRecommendTracksTopTracksFailureFallsBackToDiscovery(t *testing.T) {
	m := newMockAPI(t)
	// No /v1/me/top/tracks handler: familiar source fails.
	m.handlers["/v1/me/top/artists"] = func(t *testing.T, query url.Values) string {
		return topArtistsPage("a1")
	}
	m.handlers["/v1/artists/a1/albums"] = func(t *testing.T, query url.Values) string {
		return fmt.Sprintf(`{"items":[%s],"total":1}`, albumJSON("al1", "2020-01-01"))
	}
	m.handlers["/v1/albums/al1/tracks"] = func(t *testing.T, query url.Values) string {
		return albumTracksPage("tD1")
	}

	got, err := newTestProvider().RecommendTracks(t.Context(), nil, 10)
	if err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	if paths := trackPaths(got); len(paths) != 1 || paths[0] != "spotify:track:tD1" {
		t.Fatalf("paths = %v, want [spotify:track:tD1]", paths)
	}
}

func TestRecommendTracksTotalFailureReturnsError(t *testing.T) {
	newMockAPI(t) // no handlers at all: both source calls fail

	_, err := newTestProvider().RecommendTracks(t.Context(), nil, 10)
	if err == nil {
		t.Fatal("err = nil, want error when both sources failed")
	}
	if !strings.Contains(err.Error(), "recommend") {
		t.Errorf("err = %v, want it to mention recommend", err)
	}
}

func TestRecommendTracksLimitClamp(t *testing.T) {
	m := newMockAPI(t)
	// No handlers: any request would fail the test.

	got, err := newTestProvider().RecommendTracks(t.Context(), nil, 0)
	if err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	if len(got) != 0 {
		t.Fatalf("got %d tracks, want 0", len(got))
	}
	if n := m.calls("/v1/me/top/tracks") + m.calls("/v1/me/top/artists"); n != 0 {
		t.Errorf("made %d API calls for limit <= 0, want 0", n)
	}
}

func TestRecommendDiscoveryArtistFiltering(t *testing.T) {
	top := []spotifyArtist{
		{ID: "a1", Name: "Artist a1"},
		{ID: "a2", Name: "Artist a2"},
		{ID: "a3", Name: "Artist a3"},
		{ID: "a4", Name: "Artist a4"},
	}
	tests := []struct {
		name string
		seed []playlist.Track
		want []string
	}{
		{
			name: "artist name match filters the artist",
			seed: []playlist.Track{{Path: "spotify:track:x", Artist: "Artist a1, Someone Else"}},
			want: []string{"a2", "a3", "a4"},
		},
		{
			name: "artist id meta match filters the artist",
			seed: []playlist.Track{{Path: "spotify:track:y", ProviderMeta: map[string]string{metaSpotifyArtistIDs: "a2"}}},
			want: []string{"a1", "a3", "a4"},
		},
		{
			name: "no seed info keeps everything up to the cap",
			seed: nil,
			want: []string{"a1", "a2", "a3"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := recommendDiscoveryArtists(top, seedArtistUniverse(tt.seed))
			ids := make([]string, len(got))
			for i, a := range got {
				ids[i] = a.ID
			}
			if !slices.Equal(ids, tt.want) {
				t.Errorf("discovery artists = %v, want %v", ids, tt.want)
			}
		})
	}
}
