//go:build !windows

package spotify

import (
	"fmt"
	"net/url"
	"strings"
	"testing"

	"github.com/bjarneo/cliamp/playlist"
)

// savedTrackPage builds a /v1/me/tracks page wrapping full track objects
// (saved-track items carry the track under "track", not "item").
func savedTrackPage(total int, ids ...string) string {
	parts := make([]string, len(ids))
	for i, id := range ids {
		parts[i] = fmt.Sprintf(`{"track":%s}`, trackJSON(id))
	}
	return fmt.Sprintf(`{"items":[%s],"total":%d}`, strings.Join(parts, ","), total)
}

func TestTracksPageYourMusic(t *testing.T) {
	m := newMockAPI(t)
	m.handlers["/v1/me/tracks"] = func(t *testing.T, query url.Values) string {
		switch query.Get("offset") {
		case "0":
			if got := query.Get("limit"); got != "50" {
				t.Errorf("first page limit = %q, want 50", got)
			}
			return savedTrackPage(120, genIDs(50)...)
		case "50":
			if got := query.Get("limit"); got != "25" {
				t.Errorf("second page limit = %q, want 25", got)
			}
			return savedTrackPage(120, genIDsOffset(25, 50)...)
		default:
			if query.Get("offset") != "500" {
				t.Errorf("unexpected offset %q", query.Get("offset"))
			}
			return `{"items":[],"total":120}`
		}
	}
	p := newTestProvider()

	tracks, total, err := p.TracksPage(yourMusicID, 0, 75)
	if err != nil {
		t.Fatal(err)
	}
	if total != 120 {
		t.Errorf("total = %d, want 120", total)
	}
	if len(tracks) != 75 {
		t.Fatalf("got %d tracks, want 75", len(tracks))
	}
	if tracks[0].Path != "spotify:track:t0" || tracks[74].Path != "spotify:track:t74" {
		t.Errorf("track window edges = %q..%q, want t0..t74", tracks[0].Path, tracks[74].Path)
	}

	// Offset beyond total: empty page, total still reported.
	tracks, total, err = p.TracksPage(yourMusicID, 500, 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(tracks) != 0 || total != 120 {
		t.Errorf("beyond-end page = (%d tracks, total %d), want (0, 120)", len(tracks), total)
	}
}

func genIDs(n int) []string {
	return genIDsOffset(n, 0)
}

func genIDsOffset(n, start int) []string {
	ids := make([]string, n)
	for i := range n {
		ids[i] = fmt.Sprintf("t%d", start+i)
	}
	return ids
}

func TestTracksPagePlaylist(t *testing.T) {
	m := newMockAPI(t)
	m.handlers["/v1/playlists/pl1/items"] = func(t *testing.T, query url.Values) string {
		if got := query.Get("fields"); got != playlistItemsFields {
			t.Errorf("fields = %q, want the shared projection", got)
		}
		return `{"items":[` +
			`{"item":{"id":"t1","name":"Track t1","type":"track","uri":"spotify:track:t1","artists":[{"name":"Ringo"}],"duration_ms":100000}},` +
			`{"item":null,"track":{"id":"t2","name":"Local fallback","type":"track","uri":"spotify:track:t2","artists":[{"name":"Ringo"}],"duration_ms":100000}},` +
			`{"item":{"id":"","name":"Unavailable","type":"track","artists":[{"name":"Ringo"}]}}` +
			`],"total":3}`
	}

	tracks, total, err := newTestProvider().TracksPage("pl1", 0, 50)
	if err != nil {
		t.Fatal(err)
	}
	if total != 3 {
		t.Errorf("total = %d, want 3", total)
	}
	if len(tracks) != 2 {
		t.Fatalf("got %d tracks, want 2 (empty-ID item filtered)", len(tracks))
	}
	if tracks[1].Title != "Local fallback" {
		t.Errorf("track fallback unwrap = %q, want Local fallback", tracks[1].Title)
	}
}

func TestTracksPageTopTracks(t *testing.T) {
	m := newMockAPI(t)
	m.handlers["/v1/me/top/tracks"] = func(t *testing.T, query url.Values) string {
		if got := query.Get("time_range"); got != "short_term" {
			t.Errorf("time_range = %q, want short_term", got)
		}
		return fmt.Sprintf(`{"items":[%s,%s,%s],"total":3}`,
			trackJSON("t0"), trackJSON("t1"), trackJSON("t2"))
	}
	p := newTestProvider()

	tracks, total, err := p.TracksPage(topTracksID, 1, 2)
	if err != nil {
		t.Fatal(err)
	}
	if total != 3 {
		t.Errorf("total = %d, want 3", total)
	}
	if len(tracks) != 2 || tracks[0].Path != "spotify:track:t1" || tracks[1].Path != "spotify:track:t2" {
		t.Errorf("window = %v, want t1..t2", trackPaths(tracks))
	}

	// Cache: the second page read must not refetch.
	if _, _, err := p.TracksPage(topTracksID, 0, 2); err != nil {
		t.Fatal(err)
	}
	if n := m.calls("/v1/me/top/tracks"); n != 1 {
		t.Errorf("top tracks fetched %d times, want 1", n)
	}

	// Offset beyond the cached list: empty page, total still reported.
	tracks, total, err = p.TracksPage(topTracksID, 10, 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(tracks) != 0 || total != 3 {
		t.Errorf("beyond-end page = (%d tracks, total %d), want (0, 3)", len(tracks), total)
	}
}

func TestTracksPageRecentlyPlayedDedupe(t *testing.T) {
	m := newMockAPI(t)
	m.handlers["/v1/me/player/recently-played"] = func(t *testing.T, query url.Values) string {
		if got := query.Get("limit"); got != "50" {
			t.Errorf("limit = %q, want 50", got)
		}
		dup := `{"id":"t1","name":"Stale copy","type":"track","uri":"spotify:track:t1","artists":[{"name":"Ringo"}],"duration_ms":100000}`
		return `{"items":[` +
			`{"track":` + trackJSON("t1") + `},` +
			`{"track":` + dup + `},` +
			`{"track":` + trackJSON("t2") + `},` +
			`{"track":null}` +
			`],"total":4}`
	}
	p := newTestProvider()

	tracks, total, err := p.TracksPage(recentlyPlayedID, 0, 200)
	if err != nil {
		t.Fatal(err)
	}
	if total != 2 {
		t.Errorf("total = %d, want 2 (deduped)", total)
	}
	if len(tracks) != 2 {
		t.Fatalf("got %d tracks, want 2", len(tracks))
	}
	if tracks[0].Title != "Track t1" {
		t.Errorf("dedupe kept %q, want the newest play (Track t1)", tracks[0].Title)
	}

	// Windowing and cache behavior.
	tracks, total, err = p.TracksPage(recentlyPlayedID, 1, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(tracks) != 1 || tracks[0].Path != "spotify:track:t2" || total != 2 {
		t.Errorf("window = %v (total %d), want [t2] (total 2)", trackPaths(tracks), total)
	}
	if n := m.calls("/v1/me/player/recently-played"); n != 1 {
		t.Errorf("recently played fetched %d times, want 1", n)
	}
}

func TestTracksLoopsPagesAndPopulatesCache(t *testing.T) {
	m := newMockAPI(t)
	m.handlers["/v1/playlists/pl9/items"] = func(t *testing.T, query url.Values) string {
		offset := 0
		fmt.Sscanf(query.Get("offset"), "%d", &offset)
		if offset >= 120 {
			return `{"items":[],"total":120}`
		}
		end := min(offset+50, 120)
		ids := make([]string, 0, end-offset)
		for i := offset; i < end; i++ {
			ids = append(ids, fmt.Sprintf("t%d", i))
		}
		parts := make([]string, len(ids))
		for i, id := range ids {
			parts[i] = fmt.Sprintf(`{"item":%s}`, trackJSON(id))
		}
		return fmt.Sprintf(`{"items":[%s],"total":120}`, strings.Join(parts, ","))
	}
	p := newTestProvider()

	tracks, err := p.Tracks("pl9")
	if err != nil {
		t.Fatal(err)
	}
	if len(tracks) != 120 {
		t.Fatalf("Tracks() returned %d tracks, want 120", len(tracks))
	}
	if tracks[119].Path != "spotify:track:t119" {
		t.Errorf("last track = %q, want t119", tracks[119].Path)
	}
	if n := m.calls("/v1/playlists/pl9/items"); n != 3 {
		t.Errorf("fetched %d pages, want 3", n)
	}

	// Snapshot cache: a second full load must not hit the API.
	if _, err := p.Tracks("pl9"); err != nil {
		t.Fatal(err)
	}
	if n := m.calls("/v1/playlists/pl9/items"); n != 3 {
		t.Errorf("cached Tracks() refetched (%d calls, want 3)", n)
	}
}

func TestPlaylistsLibraryRowsAndOwned(t *testing.T) {
	m := newMockAPI(t)
	m.handlers["/v1/me"] = func(t *testing.T, query url.Values) string {
		return `{"id":"me"}`
	}
	m.handlers["/v1/me/tracks"] = func(t *testing.T, query url.Values) string {
		return `{"total":7}`
	}
	m.handlers["/v1/me/top/tracks"] = func(t *testing.T, query url.Values) string {
		return fmt.Sprintf(`{"items":[%s],"total":9}`, trackJSON("tt"))
	}
	m.handlers["/v1/me/player/recently-played"] = func(t *testing.T, query url.Values) string {
		return `{"items":[{"track":` + trackJSON("r1") + `},{"track":` + trackJSON("r2") + `},{"track":` + trackJSON("r1") + `}],"total":3}`
	}
	m.handlers["/v1/me/playlists"] = func(t *testing.T, query url.Values) string {
		return `{"items":[` +
			`{"id":"owned","name":"Owned","snapshot_id":"one","owner":{"id":"me"},"items":{"total":2}},` +
			`{"id":"followed","name":"Followed","snapshot_id":"two","owner":{"id":"other"},"items":{"total":3}}` +
			`],"total":2}`
	}

	got, err := newTestProvider().Playlists()
	if err != nil {
		t.Fatal(err)
	}

	want := []struct {
		id      string
		name    string
		section string
		count   int
		owned   bool
	}{
		{id: yourMusicID, name: "Your Music", section: "Library", count: 7},
		{id: topTracksID, name: "Top Tracks", section: "Library", count: 9},
		{id: recentlyPlayedID, name: "Recently Played", section: "Library", count: 2},
		{id: "owned", name: "Owned", section: "Your playlists", count: 2, owned: true},
		{id: "followed", name: "Followed", section: "Followed playlists", count: 3, owned: false},
	}
	if len(got) != len(want) {
		t.Fatalf("Playlists() returned %d rows, want %d: %#v", len(got), len(want), got)
	}
	for i := range want {
		row := got[i]
		if row.ID != want[i].id || row.Name != want[i].name || row.Section != want[i].section ||
			row.TrackCount != want[i].count || row.Owned != want[i].owned {
			t.Errorf("row %d = {ID:%s Name:%s Section:%s Count:%d Owned:%v}, want %+v",
				i, row.ID, row.Name, row.Section, row.TrackCount, row.Owned, want[i])
		}
	}
}

func TestTracksPageFilteredItemSpansPageBoundary(t *testing.T) {
	m := newMockAPI(t)
	filtered := `{"item":{"id":"","name":"Unavailable","type":"track","artists":[{"name":"Ringo"}]}}`
	m.handlers["/v1/playlists/pl1/items"] = func(t *testing.T, query url.Values) string {
		offset, limit := 0, 0
		fmt.Sscanf(query.Get("offset"), "%d", &offset)
		fmt.Sscanf(query.Get("limit"), "%d", &limit)
		end := min(offset+limit, 5)
		parts := make([]string, 0, end-offset)
		for i := offset; i < end; i++ {
			if i == 0 {
				parts = append(parts, filtered)
				continue
			}
			parts = append(parts, fmt.Sprintf(`{"item":%s}`, trackJSON(fmt.Sprintf("t%d", i))))
		}
		return fmt.Sprintf(`{"items":[%s],"total":5}`, strings.Join(parts, ","))
	}
	p := newTestProvider()

	// First page serves two playable tracks (t1, t2) after skipping the
	// filtered item at API position 0.
	first, total, err := p.TracksPage("pl1", 0, 2)
	if err != nil {
		t.Fatal(err)
	}
	if total != 5 || len(first) != 2 || first[0].Path != "spotify:track:t1" || first[1].Path != "spotify:track:t2" {
		t.Fatalf("first page = %v (total %d), want t1..t2", trackPaths(first), total)
	}

	// The follow-up page continues after the filtered item: no duplicates of
	// the already-served tracks and no skips.
	second, _, err := p.TracksPage("pl1", 2, 2)
	if err != nil {
		t.Fatal(err)
	}
	if got := trackPaths(second); len(got) != 2 || got[0] != "spotify:track:t3" || got[1] != "spotify:track:t4" {
		t.Fatalf("second page = %v, want t3..t4 (no duplicates, no skips)", got)
	}
}

func trackPaths(tracks []playlist.Track) []string {
	paths := make([]string, len(tracks))
	for i, t := range tracks {
		paths[i] = t.Path
	}
	return paths
}
