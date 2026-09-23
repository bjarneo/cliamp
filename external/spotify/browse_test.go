//go:build !windows

package spotify

import (
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"golang.org/x/oauth2"

	"github.com/bjarneo/cliamp/provider"
)

// apiCall records one mocked request: HTTP method and raw body ("" if none).
type apiCall struct {
	Method string
	Body   string
}

// mockAPI routes Spotify Web API requests to per-path handlers, counts calls,
// and records the last query and every (method, body) pair per path. Follows
// the DefaultTransport swap pattern from provider_nonwindows_test.go.
type mockAPI struct {
	mu       sync.Mutex
	counts   map[string]int
	last     map[string]url.Values
	recorded map[string][]apiCall
	handlers map[string]func(t *testing.T, query url.Values) string
}

func newMockAPI(t *testing.T) *mockAPI {
	t.Helper()
	m := &mockAPI{
		counts:   map[string]int{},
		last:     map[string]url.Values{},
		recorded: map[string][]apiCall{},
		handlers: map[string]func(t *testing.T, query url.Values) string{},
	}
	original := http.DefaultTransport
	http.DefaultTransport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		handler, ok := m.handlers[req.URL.Path]
		if !ok {
			return nil, fmt.Errorf("unexpected Spotify API path %q", req.URL.Path)
		}
		m.mu.Lock()
		m.counts[req.URL.Path]++
		m.last[req.URL.Path] = req.URL.Query()
		var reqBody string
		if req.Body != nil {
			if b, err := io.ReadAll(req.Body); err == nil {
				reqBody = string(b)
			}
		}
		m.recorded[req.URL.Path] = append(m.recorded[req.URL.Path], apiCall{Method: req.Method, Body: reqBody})
		m.mu.Unlock()
		body := handler(t, req.URL.Query())
		return &http.Response{
			StatusCode: http.StatusOK,
			Status:     "200 OK",
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(body)),
			Request:    req,
		}, nil
	})
	t.Cleanup(func() { http.DefaultTransport = original })
	return m
}

func (m *mockAPI) calls(path string) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.counts[path]
}

// recordedCalls returns every request made to path, in order, with method and
// raw body ("" when the request had none).
func (m *mockAPI) recordedCalls(path string) []apiCall {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.recorded[path]
}

func newTestProvider() *SpotifyProvider {
	sess := &Session{tokenSource: oauth2.StaticTokenSource(&oauth2.Token{AccessToken: "token"})}
	return New(sess, "client", 320)
}

// artistPage builds a /v1/me/following page naming artists "Artist <id>".
func artistPage(total int, ids ...string) string {
	return fmt.Sprintf(`{"artists":{"items":[%s],"total":%d}}`, artistItems(ids...), total)
}

func artistItems(ids ...string) string {
	parts := make([]string, len(ids))
	for i, id := range ids {
		parts[i] = fmt.Sprintf(`{"id":%q,"name":"Artist %s"}`, id, id)
	}
	return strings.Join(parts, ",")
}

// trackJSON builds a full or simplified track object. uri defaults to
// spotify:track:<id>; artists default to one artist named "Ringo".
func trackJSON(id string) string {
	return fmt.Sprintf(`{"id":%q,"name":"Track %s","type":"track","uri":"spotify:track:%s",`+
		`"artists":[{"id":"art1","name":"Ringo"},{"id":"art2","name":"Guest"}],`+
		`"duration_ms":180000,"track_number":1}`, id, id, id)
}

func TestArtistsPaging(t *testing.T) {
	var page1IDs []string
	for i := range 50 {
		page1IDs = append(page1IDs, fmt.Sprintf("a%d", i))
	}
	m := newMockAPI(t)
	m.handlers["/v1/me/following"] = func(t *testing.T, query url.Values) string {
		if got := query.Get("type"); got != "artist" {
			t.Errorf("type = %q, want artist", got)
		}
		if got := query.Get("limit"); got != "50" {
			t.Errorf("limit = %q, want 50", got)
		}
		switch query.Get("offset") {
		case "0":
			return artistPage(60, page1IDs...)
		case "50":
			return artistPage(60, "a50", "a51", "a52", "a53", "a54", "a55", "a56", "a57", "a58", "a59")
		default:
			t.Errorf("unexpected offset %q", query.Get("offset"))
			return `{"artists":{"items":[],"total":60}}`
		}
	}

	got, err := newTestProvider().Artists()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 60 {
		t.Fatalf("Artists() returned %d artists, want 60", len(got))
	}
	if got[0].ID != "a0" || got[0].Name != "Artist a0" {
		t.Errorf("first artist = (%q, %q), want (a0, Artist a0)", got[0].ID, got[0].Name)
	}
	if got[59].ID != "a59" {
		t.Errorf("last artist ID = %q, want a59", got[59].ID)
	}
	if n := m.calls("/v1/me/following"); n != 2 {
		t.Errorf("fetched %d pages, want 2", n)
	}
}

func TestArtistAlbums(t *testing.T) {
	m := newMockAPI(t)
	m.handlers["/v1/artists/art1/albums"] = func(t *testing.T, query url.Values) string {
		if got := query.Get("include_groups"); got != "album,single" {
			t.Errorf("include_groups = %q, want album,single", got)
		}
		return `{"items":[` +
			`{"id":"al1","name":"Debut","release_date":"1994-03-29","total_tracks":12,` +
			`"artists":[{"id":"art1","name":"Ringo"},{"id":"art2","name":"Guest"}]},` +
			`{"id":"al2","name":"Single","release_date":"2020","total_tracks":2,` +
			`"artists":[{"id":"art1","name":"Ringo"}]}` +
			`],"total":2}`
	}

	got, err := newTestProvider().ArtistAlbums("art1")
	if err != nil {
		t.Fatal(err)
	}
	want := []provider.AlbumInfo{
		{ID: "al1", Name: "Debut", Artist: "Ringo", ArtistID: "art1", Year: 1994, TrackCount: 12},
		{ID: "al2", Name: "Single", Artist: "Ringo", ArtistID: "art1", Year: 2020, TrackCount: 2},
	}
	if len(got) != len(want) {
		t.Fatalf("ArtistAlbums() returned %d albums, want %d: %#v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("album %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}

const savedAlbumsJSON = `{"items":[` +
	`{"added_at":"2024-01-04T00:00:00Z","album":{"id":"b1","name":"Zebra","artists":[{"id":"ab","name":"Bartist"}],"release_date":"2001-05-05","total_tracks":10}},` +
	`{"added_at":"2024-01-03T00:00:00Z","album":{"id":"b2","name":"Alpha","artists":[{"id":"aa","name":"Aartist"}],"release_date":"1990-01-01","total_tracks":9}},` +
	`{"added_at":"2024-01-02T00:00:00Z","album":{"id":"b3","name":"Mid","artists":[{"id":"aa","name":"Aartist"}],"release_date":"2010-02-02","total_tracks":8}},` +
	`{"added_at":"2024-01-01T00:00:00Z","album":{"id":"b4","name":"Beta","artists":[{"id":"ac","name":"Cartist"}],"release_date":"2010-03-03","total_tracks":7}}` +
	`],"total":4}`

func TestAlbumListSortsAndWindow(t *testing.T) {
	tests := []struct {
		name     string
		sortType string
		offset   int
		size     int
		wantIDs  []string
		wantErr  bool
	}{
		{name: "recent is api order", sortType: SortRecent, size: 10, wantIDs: []string{"b1", "b2", "b3", "b4"}},
		{name: "empty sort defaults to recent", sortType: "", size: 10, wantIDs: []string{"b1", "b2", "b3", "b4"}},
		{name: "title", sortType: SortTitle, size: 10, wantIDs: []string{"b2", "b4", "b3", "b1"}},
		{name: "artist then title", sortType: SortArtist, size: 10, wantIDs: []string{"b2", "b3", "b1", "b4"}},
		{name: "year newest first", sortType: SortYear, size: 10, wantIDs: []string{"b4", "b3", "b1", "b2"}},
		{name: "window offset+size", sortType: SortTitle, offset: 1, size: 2, wantIDs: []string{"b4", "b3"}},
		{name: "offset beyond end", sortType: SortTitle, offset: 4, size: 2, wantIDs: nil},
		{name: "unknown sort", sortType: "nope", size: 10, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := newMockAPI(t)
			m.handlers["/v1/me/albums"] = func(t *testing.T, query url.Values) string {
				return savedAlbumsJSON
			}

			got, err := newTestProvider().AlbumList(tt.sortType, tt.offset, tt.size)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("AlbumList(%q) err = nil, want error", tt.sortType)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			gotIDs := make([]string, len(got))
			for i, a := range got {
				gotIDs[i] = a.ID
			}
			if len(gotIDs) == 0 && len(tt.wantIDs) == 0 {
				return
			}
			if strings.Join(gotIDs, ",") != strings.Join(tt.wantIDs, ",") {
				t.Errorf("AlbumList(%q, %d, %d) = %v, want %v", tt.sortType, tt.offset, tt.size, gotIDs, tt.wantIDs)
			}
		})
	}
}

func TestAlbumListCacheHit(t *testing.T) {
	m := newMockAPI(t)
	m.handlers["/v1/me/albums"] = func(t *testing.T, query url.Values) string {
		return savedAlbumsJSON
	}
	p := newTestProvider()

	if _, err := p.AlbumList(SortRecent, 0, 2); err != nil {
		t.Fatal(err)
	}
	if n := m.calls("/v1/me/albums"); n != 1 {
		t.Fatalf("first AlbumList fetched %d times, want 1", n)
	}

	// Different sort and window: served from the cached raw list.
	got, err := p.AlbumList(SortTitle, 0, 4)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 4 || got[0].ID != "b2" {
		t.Errorf("cached AlbumList = %v, want title order starting with b2", got)
	}
	if n := m.calls("/v1/me/albums"); n != 1 {
		t.Errorf("cached AlbumList refetched (%d calls, want 1)", n)
	}

	// Invalidation forces a refetch.
	p.invalidateAlbumCache()
	if _, err := p.AlbumList(SortRecent, 0, 2); err != nil {
		t.Fatal(err)
	}
	if n := m.calls("/v1/me/albums"); n != 2 {
		t.Errorf("after invalidate AlbumList fetched %d times, want 2", n)
	}
}

func TestAlbumTracks(t *testing.T) {
	m := newMockAPI(t)
	m.handlers["/v1/albums/alb1"] = func(t *testing.T, query url.Values) string {
		return `{"id":"alb1","name":"Cool Album","release_date":"1999-12-31","total_tracks":60,` +
			`"artists":[{"id":"art1","name":"Ringo"}],` +
			`"images":[{"url":"https://i.scdn.co/640","height":640,"width":640},` +
			`{"url":"https://i.scdn.co/300","height":300,"width":300}]}`
	}
	m.handlers["/v1/albums/alb1/tracks"] = func(t *testing.T, query url.Values) string {
		if got := query.Get("limit"); got != "50" {
			t.Errorf("limit = %q, want 50", got)
		}
		offset := 0
		fmt.Sscanf(query.Get("offset"), "%d", &offset)
		if offset >= 60 {
			return `{"items":[],"total":60}`
		}
		ids := make([]string, 0, 50)
		for i := offset; i < offset+50 && i < 60; i++ {
			ids = append(ids, fmt.Sprintf("t%d", i))
		}
		parts := make([]string, len(ids))
		for i, id := range ids {
			parts[i] = trackJSON(id)
		}
		return fmt.Sprintf(`{"items":[%s],"total":60}`, strings.Join(parts, ","))
	}

	got, err := newTestProvider().AlbumTracks("alb1")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 60 {
		t.Fatalf("AlbumTracks() returned %d tracks, want 60", len(got))
	}
	if n := m.calls("/v1/albums/alb1"); n != 1 {
		t.Errorf("album metadata fetched %d times, want 1", n)
	}
	if n := m.calls("/v1/albums/alb1/tracks"); n != 2 {
		t.Errorf("album tracks fetched %d pages, want 2", n)
	}

	first := got[0]
	if first.Path != "spotify:track:t0" {
		t.Errorf("Path = %q, want spotify:track:t0", first.Path)
	}
	if first.Title != "Track t0" {
		t.Errorf("Title = %q, want Track t0", first.Title)
	}
	if first.Artist != "Ringo, Guest" {
		t.Errorf("Artist = %q, want 'Ringo, Guest'", first.Artist)
	}
	if first.Album != "Cool Album" {
		t.Errorf("Album = %q, want Cool Album", first.Album)
	}
	if first.AlbumArtURL != "https://i.scdn.co/300" {
		t.Errorf("AlbumArtURL = %q, want https://i.scdn.co/300", first.AlbumArtURL)
	}
	if first.Year != 1999 {
		t.Errorf("Year = %d, want 1999", first.Year)
	}
	if first.DurationSecs != 180 {
		t.Errorf("DurationSecs = %d, want 180", first.DurationSecs)
	}
	if got := first.ProviderMeta[metaSpotifyID]; got != "t0" {
		t.Errorf("ProviderMeta[%q] = %q, want t0", metaSpotifyID, got)
	}
}

func TestAlbumSortTypes(t *testing.T) {
	want := []provider.SortType{
		{ID: SortRecent, Label: "Recently saved"},
		{ID: SortTitle, Label: "Title"},
		{ID: SortArtist, Label: "Artist"},
		{ID: SortYear, Label: "Year"},
	}
	got := newTestProvider().AlbumSortTypes()
	if len(got) != len(want) {
		t.Fatalf("AlbumSortTypes() returned %d, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("sort %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}

func TestSaveAndDefaultAlbumSort(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CLIAMP_CONFIG_DIR", dir)

	p := newTestProvider()
	if got := p.DefaultAlbumSort(); got != SortRecent {
		t.Errorf("DefaultAlbumSort() = %q, want %q", got, SortRecent)
	}
	if err := p.SaveAlbumSort(SortTitle); err != nil {
		t.Fatalf("SaveAlbumSort: %v", err)
	}
	if got := p.DefaultAlbumSort(); got != SortTitle {
		t.Errorf("DefaultAlbumSort() after save = %q, want %q", got, SortTitle)
	}
	if err := p.SaveAlbumSort("nope"); err == nil {
		t.Error("SaveAlbumSort(nope) err = nil, want error")
	}
	if err := p.SaveAlbumSort(""); err != nil {
		t.Fatalf("SaveAlbumSort(empty): %v", err)
	}

	// A fresh provider reads the persisted sort from the config file.
	fresh := newTestProvider()
	if got := fresh.DefaultAlbumSort(); got != SortRecent {
		t.Errorf("fresh DefaultAlbumSort() = %q, want %q", got, SortRecent)
	}
	data, err := os.ReadFile(filepath.Join(dir, "config.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `album_sort = "recent"`) {
		t.Errorf("config file missing persisted sort: %s", data)
	}

	// A garbage persisted value falls back to the default.
	if err := writeConfig(t, dir, "[spotify]\nalbum_sort = \"garbage\"\n"); err != nil {
		t.Fatal(err)
	}
	if got := newTestProvider().DefaultAlbumSort(); got != SortRecent {
		t.Errorf("DefaultAlbumSort() with garbage config = %q, want %q", got, SortRecent)
	}
}

func writeConfig(t *testing.T, dir, content string) error {
	t.Helper()
	return os.WriteFile(filepath.Join(dir, "config.toml"), []byte(content), 0o600)
}
