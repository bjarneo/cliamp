package spotify

import (
	"encoding/json"
	"fmt"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/bjarneo/cliamp/provider"
)

// popTrackJSON builds a full track object (search-result shape) with a
// popularity score and art1/Ringo as the sole artist.
func popTrackJSON(id string, popularity int) string {
	return fmt.Sprintf(`{"id":%q,"name":"Track %s","type":"track","uri":"spotify:track:%s",`+
		`"artists":[{"id":"art1","name":"Ringo"}],"album":{"name":"Album %s","release_date":"2019-02-02"},`+
		`"duration_ms":180000,"track_number":1,"popularity":%d}`, id, id, id, id, popularity)
}

// foreignTrackJSON builds a track object by a different artist.
func foreignTrackJSON(id string) string {
	return fmt.Sprintf(`{"id":%q,"name":"Track %s","type":"track","uri":"spotify:track:%s",`+
		`"artists":[{"id":"zz","name":"Someone Else"}],"duration_ms":180000,"track_number":1}`, id, id, id)
}

// containsHandler builds a /v1/me/library/contains reply marking exactly the
// given URIs as contained.
func containsHandler(liked map[string]bool) func(t *testing.T, query url.Values) string {
	return func(t *testing.T, query url.Values) string {
		uris := strings.Split(query.Get("uris"), ",")
		states := make([]bool, len(uris))
		for i, uri := range uris {
			states[i] = liked[uri]
		}
		b, err := json.Marshal(states)
		if err != nil {
			t.Fatalf("marshal contains states: %v", err)
		}
		return string(b)
	}
}

// artistHeaderJSON builds the /v1/artists/{id} body.
func artistHeaderJSON(id, name string, followers int, genres ...string) string {
	return fmt.Sprintf(`{"id":%q,"name":%q,"genres":[%s],"followers":{"href":null,"total":%d}}`,
		id, name, `"`+strings.Join(genres, `","`)+`"`, followers)
}

func TestArtistDetailHappyPath(t *testing.T) {
	m := newMockAPI(t)
	m.handlers["/v1/artists/art1"] = func(t *testing.T, query url.Values) string {
		return artistHeaderJSON("art1", "Ringo", 1234, "rock", "pop")
	}

	// Discography: 12 albums over two pages of 10 (the per-request max).
	page1 := make([]string, 0, 10)
	for i := 1; i <= 10; i++ {
		page1 = append(page1, albumJSON(fmt.Sprintf("al%d", i), fmt.Sprintf("%d-01-01", 2000+i)))
	}
	page2 := albumJSON("al11", "2011-01-01") + "," + albumJSON("al12", "2012-01-01")
	m.handlers["/v1/artists/art1/albums"] = func(t *testing.T, query url.Values) string {
		if got := query.Get("include_groups"); got != "album,single" {
			t.Errorf("include_groups = %q, want album,single", got)
		}
		if got := query.Get("limit"); got != "10" {
			t.Errorf("limit = %q, want 10 (the per-request max)", got)
		}
		switch query.Get("offset") {
		case "0":
			return fmt.Sprintf(`{"items":[%s],"total":12}`, strings.Join(page1, ","))
		case "10":
			return fmt.Sprintf(`{"items":[%s],"total":12}`, page2)
		default:
			t.Errorf("unexpected offset %q", query.Get("offset"))
			return `{"items":[],"total":12}`
		}
	}

	// Album tracks: simplified track objects (no popularity).
	albumTracks := map[string]string{
		"al1": albumTracksPage("tA"),
		"al2": albumTracksPage("tB"),
		"al3": albumTracksPage("tC"),
	}
	for i := 4; i <= 12; i++ {
		albumTracks[fmt.Sprintf("al%d", i)] = albumTracksPage(fmt.Sprintf("tX%d", i))
	}
	for id, body := range albumTracks {
		m.handlers["/v1/albums/"+id+"/tracks"] = func(t *testing.T, query url.Values) string {
			return body
		}
	}

	// Search: one page, three full track objects carrying popularity.
	m.handlers["/v1/search"] = func(t *testing.T, query url.Values) string {
		if got := query.Get("q"); got != `artist:"Ringo"` {
			t.Errorf("q = %q, want artist:\"Ringo\"", got)
		}
		if got := query.Get("type"); got != "track" {
			t.Errorf("type = %q, want track", got)
		}
		if got := query.Get("limit"); got != "10" {
			t.Errorf("limit = %q, want 10 (the endpoint max)", got)
		}
		return `{"tracks":{"items":[` +
			popTrackJSON("tS1", 80) + "," + popTrackJSON("tS2", 85) + "," + popTrackJSON("tS3", 90) +
			`],"total":3}}`
	}

	m.handlers["/v1/me/library/contains"] = containsHandler(map[string]bool{
		"spotify:track:tS3": true,
		"spotify:track:tA":  true,
	})

	detail, err := newTestProvider().ArtistDetail("art1")
	if err != nil {
		t.Fatal(err)
	}

	// Header: genres + followers come from the deprecated-but-present fields.
	if detail.Info != (provider.ArtistInfo{ID: "art1", Name: "Ringo", AlbumCount: 12}) {
		t.Errorf("Info = %+v, want {art1 Ringo 12}", detail.Info)
	}
	if !slices.Equal(detail.Genres, []string{"rock", "pop"}) {
		t.Errorf("Genres = %v, want [rock pop]", detail.Genres)
	}
	if detail.Followers != 1234 {
		t.Errorf("Followers = %d, want 1234", detail.Followers)
	}

	// Discography: both pages mapped via albumFromSpotify.
	if len(detail.Discography) != 12 {
		t.Fatalf("Discography has %d albums, want 12", len(detail.Discography))
	}
	wantFirst := provider.AlbumInfo{ID: "al1", Name: "Album al1", Artist: "Artist a1", ArtistID: "a1", Year: 2001, TrackCount: 2}
	if detail.Discography[0] != wantFirst {
		t.Errorf("Discography[0] = %+v, want %+v", detail.Discography[0], wantFirst)
	}
	if n := m.calls("/v1/artists/art1/albums"); n != 2 {
		t.Errorf("fetched %d album pages, want 2", n)
	}

	// Popular: popularity desc (search items are the only ones with scores),
	// then zero-popularity album tracks in pool (discography) order, capped
	// at 10.
	paths := trackPaths(detail.Popular)
	wantPaths := []string{
		"spotify:track:tS3", "spotify:track:tS2", "spotify:track:tS1",
		"spotify:track:tA", "spotify:track:tB", "spotify:track:tC",
		"spotify:track:tX4", "spotify:track:tX5", "spotify:track:tX6", "spotify:track:tX7",
	}
	if !slices.Equal(paths, wantPaths) {
		t.Fatalf("Popular paths = %v, want %v", paths, wantPaths)
	}

	// Popularity meta only on tracks that report a score.
	for i, want := range []string{"90", "85", "80", "", "", "", "", "", "", ""} {
		if got := detail.Popular[i].ProviderMeta[provider.MetaSpotifyPopularity]; got != want {
			t.Errorf("Popular[%d] popularity meta = %q, want %q", i, got, want)
		}
	}

	// Liked marks only on contained tracks.
	if got := detail.Popular[0].ProviderMeta[provider.MetaSpotifyLiked]; got != "true" {
		t.Errorf("Popular[0] liked meta = %q, want true (tS3 contained)", got)
	}
	if got := detail.Popular[3].ProviderMeta[provider.MetaSpotifyLiked]; got != "true" {
		t.Errorf("Popular[3] liked meta = %q, want true (tA contained)", got)
	}
	if got := detail.Popular[1].ProviderMeta[provider.MetaSpotifyLiked]; got != "" {
		t.Errorf("Popular[1] liked meta = %q, want none (tS2 not contained)", got)
	}

	// Album-sourced candidates carry the parent album's metadata (tA is on
	// al1, released 2001); search candidates keep their own album fields
	// (tS3's album was released 2019).
	if got := detail.Popular[3]; got.Album != "Album al1" || got.Year != 2001 || got.AlbumArtURL != "https://img/al1" {
		t.Errorf("Popular[3] = {Album:%q Year:%d Art:%q}, want {Album al1 2001 https://img/al1}",
			got.Album, got.Year, got.AlbumArtURL)
	}
	if got := detail.Popular[0]; got.Album != "Album tS3" || got.Year != 2019 {
		t.Errorf("Popular[0] = {Album:%q Year:%d}, want {Album tS3 2019}", got.Album, got.Year)
	}
}

func TestArtistDetailSearchPaging(t *testing.T) {
	m := newMockAPI(t)
	m.handlers["/v1/artists/art1"] = func(t *testing.T, query url.Values) string {
		return artistHeaderJSON("art1", "Ringo", 10)
	}
	m.handlers["/v1/artists/art1/albums"] = func(t *testing.T, query url.Values) string {
		return `{"items":[],"total":0}`
	}

	// 30 results, 10 per page: three offset pages must be requested.
	items := make([]string, 0, 30)
	for i := 1; i <= 30; i++ {
		items = append(items, popTrackJSON(fmt.Sprintf("tP%d", i), 100-i))
	}
	var offsets []string
	m.handlers["/v1/search"] = func(t *testing.T, query url.Values) string {
		off, err := strconv.Atoi(query.Get("offset"))
		if err != nil {
			t.Errorf("offset = %q, not numeric", query.Get("offset"))
			return `{"tracks":{"items":[],"total":0}}`
		}
		offsets = append(offsets, query.Get("offset"))
		return fmt.Sprintf(`{"tracks":{"items":[%s],"total":30}}`, strings.Join(items[off:off+10], ","))
	}
	m.handlers["/v1/me/library/contains"] = containsHandler(nil)

	detail, err := newTestProvider().ArtistDetail("art1")
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(offsets, []string{"0", "10", "20"}) {
		t.Errorf("search offsets = %v, want [0 10 20]", offsets)
	}
	if n := m.calls("/v1/search"); n != 3 {
		t.Errorf("fetched %d search pages, want 3", n)
	}
	// Top 10 by popularity are exactly page 1, in order.
	want := make([]string, 10)
	for i := 1; i <= 10; i++ {
		want[i-1] = "spotify:track:tP" + strconv.Itoa(i)
	}
	if paths := trackPaths(detail.Popular); !slices.Equal(paths, want) {
		t.Errorf("Popular paths = %v, want %v", paths, want)
	}
}

func TestArtistDetailDedupeAndArtistFilter(t *testing.T) {
	m := newMockAPI(t)
	m.handlers["/v1/artists/art1"] = func(t *testing.T, query url.Values) string {
		return artistHeaderJSON("art1", "Ringo", 10)
	}
	m.handlers["/v1/artists/art1/albums"] = func(t *testing.T, query url.Values) string {
		return fmt.Sprintf(`{"items":[%s,%s],"total":2}`, albumJSON("al1", "2001-01-01"), albumJSON("al2", "2002-01-01"))
	}
	// t1 appears on both albums; t2 only on the first.
	m.handlers["/v1/albums/al1/tracks"] = func(t *testing.T, query url.Values) string {
		return albumTracksPage("t1", "t2")
	}
	m.handlers["/v1/albums/al2/tracks"] = func(t *testing.T, query url.Values) string {
		return albumTracksPage("t1")
	}
	// Search repeats t1 and t2 (with popularity scores that must NOT
	// override the album copies) and adds t3 plus a foreign-artist track.
	m.handlers["/v1/search"] = func(t *testing.T, query url.Values) string {
		return `{"tracks":{"items":[` +
			popTrackJSON("t1", 99) + "," + foreignTrackJSON("tF") + "," + popTrackJSON("t2", 70) + "," + popTrackJSON("t3", 60) +
			`],"total":4}}`
	}
	m.handlers["/v1/me/library/contains"] = containsHandler(map[string]bool{"spotify:track:t1": true})

	detail, err := newTestProvider().ArtistDetail("art1")
	if err != nil {
		t.Fatal(err)
	}

	paths := trackPaths(detail.Popular)
	want := []string{"spotify:track:t3", "spotify:track:t1", "spotify:track:t2"}
	if !slices.Equal(paths, want) {
		t.Fatalf("Popular paths = %v, want %v (deduped, foreign track filtered)", paths, want)
	}
	// The kept t1/t2 copies are the album ones: no popularity meta, but the
	// liked mark from the contains batch lands on the pool URI.
	if got := detail.Popular[1].ProviderMeta[provider.MetaSpotifyPopularity]; got != "" {
		t.Errorf("t1 popularity meta = %q, want none (album copy wins over search duplicate)", got)
	}
	if got := detail.Popular[2].ProviderMeta[provider.MetaSpotifyPopularity]; got != "" {
		t.Errorf("t2 popularity meta = %q, want none (album copy wins over search duplicate)", got)
	}
	if got := detail.Popular[0].ProviderMeta[provider.MetaSpotifyPopularity]; got != "60" {
		t.Errorf("t3 popularity meta = %q, want 60", got)
	}
	if got := detail.Popular[1].ProviderMeta[provider.MetaSpotifyLiked]; got != "true" {
		t.Errorf("t1 liked meta = %q, want true", got)
	}
}

func TestArtistDetailLikedBatchingAtForty(t *testing.T) {
	m := newMockAPI(t)
	m.handlers["/v1/artists/art1"] = func(t *testing.T, query url.Values) string {
		return artistHeaderJSON("art1", "Ringo", 10)
	}
	m.handlers["/v1/artists/art1/albums"] = func(t *testing.T, query url.Values) string {
		return fmt.Sprintf(`{"items":[%s],"total":1}`, albumJSON("alBig", "2001-01-01"))
	}
	ids := make([]string, 0, 45)
	for i := 1; i <= 45; i++ {
		ids = append(ids, fmt.Sprintf("tL%d", i))
	}
	m.handlers["/v1/albums/alBig/tracks"] = func(t *testing.T, query url.Values) string {
		return albumTracksPage(ids...)
	}
	m.handlers["/v1/search"] = func(t *testing.T, query url.Values) string {
		return `{"tracks":{"items":[],"total":0}}`
	}

	var chunks []string
	m.handlers["/v1/me/library/contains"] = func(t *testing.T, query url.Values) string {
		uris := strings.Split(query.Get("uris"), ",")
		chunks = append(chunks, query.Get("uris"))
		states := make([]bool, len(uris))
		for i := range states {
			states[i] = true // everything contained
		}
		b, err := json.Marshal(states)
		if err != nil {
			t.Fatalf("marshal contains states: %v", err)
		}
		return string(b)
	}

	detail, err := newTestProvider().ArtistDetail("art1")
	if err != nil {
		t.Fatal(err)
	}

	if len(chunks) != 2 {
		t.Fatalf("contains called %d times, want 2 (45 URIs at 40 per call)", len(chunks))
	}
	if got := len(strings.Split(chunks[0], ",")); got != 40 {
		t.Errorf("first contains chunk has %d uris, want 40", got)
	}
	if got := len(strings.Split(chunks[1], ",")); got != 5 {
		t.Errorf("second contains chunk has %d uris, want 5", got)
	}
	if first := strings.Split(chunks[0], ",")[0]; first != "spotify:track:tL1" {
		t.Errorf("first uri of first chunk = %q, want spotify:track:tL1", first)
	}
	// The top-10 (zero-popularity pool order) all carry the liked mark.
	for i, tr := range detail.Popular {
		if got := tr.ProviderMeta[provider.MetaSpotifyLiked]; got != "true" {
			t.Errorf("Popular[%d] liked meta = %q, want true", i, got)
		}
	}
}

func TestArtistDetailContainsFailureSkipsMarks(t *testing.T) {
	m := newMockAPI(t)
	m.handlers["/v1/artists/art1"] = func(t *testing.T, query url.Values) string {
		return artistHeaderJSON("art1", "Ringo", 10)
	}
	m.handlers["/v1/artists/art1/albums"] = func(t *testing.T, query url.Values) string {
		return fmt.Sprintf(`{"items":[%s],"total":1}`, albumJSON("al1", "2001-01-01"))
	}
	m.handlers["/v1/albums/al1/tracks"] = func(t *testing.T, query url.Values) string {
		return albumTracksPage("t1")
	}
	m.handlers["/v1/search"] = func(t *testing.T, query url.Values) string {
		return `{"tracks":{"items":[` + popTrackJSON("t2", 50) + `],"total":1}}`
	}
	// No /v1/me/library/contains handler: the contains call fails.

	detail, err := newTestProvider().ArtistDetail("art1")
	if err != nil {
		t.Fatalf("err = %v, want nil (contains failure is non-fatal)", err)
	}
	if len(detail.Popular) != 2 {
		t.Fatalf("Popular has %d tracks, want 2", len(detail.Popular))
	}
	for i, tr := range detail.Popular {
		if got := tr.ProviderMeta[provider.MetaSpotifyLiked]; got != "" {
			t.Errorf("Popular[%d] liked meta = %q, want none (contains failed)", i, got)
		}
	}
}

func TestArtistDetailPoolFailureLeavesPopularEmpty(t *testing.T) {
	m := newMockAPI(t)
	m.handlers["/v1/artists/art1"] = func(t *testing.T, query url.Values) string {
		return artistHeaderJSON("art1", "Ringo", 10)
	}
	m.handlers["/v1/artists/art1/albums"] = func(t *testing.T, query url.Values) string {
		return fmt.Sprintf(`{"items":[%s],"total":1}`, albumJSON("al1", "2001-01-01"))
	}
	// No /v1/albums/al1/tracks and no /v1/search handler: both pool sources
	// fail; the detail must still come back.

	detail, err := newTestProvider().ArtistDetail("art1")
	if err != nil {
		t.Fatalf("err = %v, want nil (popular-pool failures are non-fatal)", err)
	}
	if len(detail.Popular) != 0 {
		t.Errorf("Popular has %d tracks, want 0", len(detail.Popular))
	}
	if detail.Info.Name != "Ringo" || len(detail.Discography) != 1 {
		t.Errorf("header/discography = %+v / %d albums, want Ringo / 1", detail.Info, len(detail.Discography))
	}
}

func TestArtistDetailHeaderFailureReturnsError(t *testing.T) {
	m := newMockAPI(t) // no /v1/artists/art1 handler: the header call fails

	_, err := newTestProvider().ArtistDetail("art1")
	if err == nil {
		t.Fatal("err = nil, want error when the header call failed")
	}
	if !strings.Contains(err.Error(), "artist detail") {
		t.Errorf("err = %v, want it to mention artist detail", err)
	}
	if n := m.calls("/v1/artists/art1/albums"); n != 0 {
		t.Errorf("made %d discography calls, want 0 (header failure short-circuits)", n)
	}
}
