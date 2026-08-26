package lyrion

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/bjarneo/cliamp/config"
	"github.com/bjarneo/cliamp/playlist"
	"github.com/bjarneo/cliamp/provider"
)

// capture records the decoded envelope of the last request a test server saw.
type capture struct {
	Command []any
	Player  string
	Auth    string
}

// newServer returns a Client pointed at a test server that replies with body
// for every request, plus the capture the server writes each request into.
func newServer(t *testing.T, body string) (*Client, *capture) {
	t.Helper()
	got := &capture{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got.Auth = r.Header.Get("Authorization")
		raw, _ := io.ReadAll(r.Body)
		var env struct {
			Method string `json:"method"`
			Params []any  `json:"params"`
		}
		if err := json.Unmarshal(raw, &env); err != nil {
			t.Errorf("request body is not JSON: %v", err)
		}
		if env.Method != "slim.request" {
			t.Errorf("method = %q, want slim.request", env.Method)
		}
		if len(env.Params) == 2 {
			got.Player, _ = env.Params[0].(string)
			got.Command, _ = env.Params[1].([]any)
		} else {
			t.Errorf("params length = %d, want 2", len(env.Params))
		}
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, body)
	}))
	t.Cleanup(srv.Close)
	return New(srv.URL, "", ""), got
}

// cmdString renders a captured command for substring assertions.
func cmdString(cmd []any) string {
	parts := make([]string, 0, len(cmd))
	for _, c := range cmd {
		parts = append(parts, strings.TrimSuffix(strings.TrimSuffix(jsonScalar(c), ".0"), "\n"))
	}
	return strings.Join(parts, " ")
}

func jsonScalar(v any) string {
	b, _ := json.Marshal(v)
	s := string(b)
	return strings.Trim(s, `"`)
}

// --- 2.1 construction -------------------------------------------------------

func TestName(t *testing.T) {
	if got := New("http://nas:9000", "", "").Name(); got != "lyrion" {
		t.Errorf("Name() = %q, want lyrion", got)
	}
}

func TestNewNormalisesTrailingSlashes(t *testing.T) {
	for _, in := range []string{"http://nas:9000", "http://nas:9000/", "http://nas:9000///", " http://nas:9000/ "} {
		if got := New(in, "", "").url; got != "http://nas:9000" {
			t.Errorf("New(%q).url = %q, want http://nas:9000", in, got)
		}
	}
}

func TestNewFromConfig(t *testing.T) {
	if c := NewFromConfig(config.LyrionConfig{}); c != nil {
		t.Error("NewFromConfig with no URL should return nil")
	}
	c := NewFromConfig(config.LyrionConfig{URL: "http://nas:9000/", User: "bob", Password: "pw"})
	if c == nil {
		t.Fatal("NewFromConfig with a URL returned nil")
	}
	if c.url != "http://nas:9000" || c.user != "bob" || c.password != "pw" {
		t.Errorf("NewFromConfig = %+v", c)
	}
}

func TestNewFromEnv(t *testing.T) {
	t.Setenv("LYRION_URL", "")
	if c := NewFromEnv(); c != nil {
		t.Error("NewFromEnv with no LYRION_URL should return nil")
	}
	t.Setenv("LYRION_URL", "http://nas:9000")
	t.Setenv("LYRION_USER", "bob")
	t.Setenv("LYRION_PASS", "pw")
	c := NewFromEnv()
	if c == nil {
		t.Fatal("NewFromEnv returned nil with LYRION_URL set")
	}
	if c.url != "http://nas:9000" || c.user != "bob" {
		t.Errorf("NewFromEnv = %+v", c)
	}
}

// --- 2.2 envelope and auth --------------------------------------------------

func TestRequestEnvelopeUsesEmptyPlayerID(t *testing.T) {
	c, got := newServer(t, `{"result":{"artists_loop":[]}}`)
	if _, err := c.Artists(); err != nil {
		t.Fatalf("Artists() error = %v", err)
	}
	if got.Player != "" {
		t.Errorf("player id = %q, want empty (library queries address no player)", got.Player)
	}
	if len(got.Command) == 0 || got.Command[0] != "artists" {
		t.Errorf("command = %v, want it to start with artists", got.Command)
	}
}

func TestRequestSendsBasicAuthWhenConfigured(t *testing.T) {
	c, got := newServer(t, `{"result":{"artists_loop":[]}}`)
	c.user, c.password = "bob", "pw"
	if _, err := c.Artists(); err != nil {
		t.Fatalf("Artists() error = %v", err)
	}
	if !strings.HasPrefix(got.Auth, "Basic ") {
		t.Errorf("Authorization = %q, want a Basic credential", got.Auth)
	}
}

func TestRequestOmitsAuthWhenUnconfigured(t *testing.T) {
	c, got := newServer(t, `{"result":{"artists_loop":[]}}`)
	if _, err := c.Artists(); err != nil {
		t.Fatalf("Artists() error = %v", err)
	}
	if got.Auth != "" {
		t.Errorf("Authorization = %q, want none for an unprotected server", got.Auth)
	}
}

// --- 2.3 error mapping ------------------------------------------------------

func TestRequestErrors(t *testing.T) {
	tests := []struct {
		name   string
		status int
		body   string
		want   string
	}{
		{"unauthorized", http.StatusUnauthorized, "", "authentication failed"},
		{"forbidden", http.StatusForbidden, "", "authentication failed"},
		{"server error", http.StatusInternalServerError, "", "http status"},
		{"malformed body", http.StatusOK, "not json at all", "decode response"},
		{"no result", http.StatusOK, `{"id":1}`, "no result"},
		{"server-reported error", http.StatusOK, `{"error":"unknown command"}`, "unknown command"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tt.status)
				io.WriteString(w, tt.body)
			}))
			defer srv.Close()
			_, err := New(srv.URL, "", "").Artists()
			if err == nil {
				t.Fatal("expected an error")
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Errorf("error = %v, want it to mention %q", err, tt.want)
			}
			if !strings.Contains(err.Error(), srv.URL) {
				t.Errorf("error = %v, want it to name the server", err)
			}
		})
	}
}

func TestRequestUnreachableServer(t *testing.T) {
	// Port 1 on the loopback interface refuses connections.
	_, err := New("http://127.0.0.1:1", "", "").Artists()
	if err == nil {
		t.Fatal("expected an error for an unreachable server")
	}
	if !strings.Contains(err.Error(), "127.0.0.1:1") {
		t.Errorf("error = %v, want it to name the server", err)
	}
}

// --- 3. browsing ------------------------------------------------------------

func TestPlaylists(t *testing.T) {
	c, _ := newServer(t, `{"result":{"count":2,"playlists_loop":[
		{"id":3,"playlist":"Morning"},
		{"id":"7","playlist":"Focus"}]}}`)
	got, err := c.Playlists()
	if err != nil {
		t.Fatalf("Playlists() error = %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d playlists, want 2", len(got))
	}
	// LMS sends ids as numbers or strings interchangeably; both must survive.
	if got[0].ID != "3" || got[0].Name != "Morning" {
		t.Errorf("playlist[0] = %+v", got[0])
	}
	if got[1].ID != "7" || got[1].Name != "Focus" {
		t.Errorf("playlist[1] = %+v", got[1])
	}
}

func TestTracksPreservesStoredOrder(t *testing.T) {
	c, got := newServer(t, `{"result":{"playlisttracks_loop":[
		{"id":1,"title":"First"},{"id":2,"title":"Second"},{"id":3,"title":"Third"}]}}`)
	tracks, err := c.Tracks("42")
	if err != nil {
		t.Fatalf("Tracks() error = %v", err)
	}
	want := []string{"First", "Second", "Third"}
	for i, w := range want {
		if tracks[i].Title != w {
			t.Errorf("track[%d].Title = %q, want %q", i, tracks[i].Title, w)
		}
	}
	if !strings.Contains(cmdString(got.Command), "playlist_id:42") {
		t.Errorf("command = %v, want it to scope to playlist_id:42", got.Command)
	}
}

func TestTracksPaginatesContainerResults(t *testing.T) {
	var offsets []int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var env struct {
			Params []json.RawMessage `json:"params"`
		}
		if err := json.NewDecoder(r.Body).Decode(&env); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		var command []any
		if err := json.Unmarshal(env.Params[1], &command); err != nil {
			t.Fatalf("decode command: %v", err)
		}
		offset := int(command[2].(float64))
		offsets = append(offsets, offset)
		id := offset + 1
		io.WriteString(w, `{"result":{"count":2,"playlisttracks_loop":[{"id":`+strconv.Itoa(id)+`,"title":"Track"}]}}`)
	}))
	defer srv.Close()

	tracks, err := New(srv.URL, "", "").Tracks("42")
	if err != nil {
		t.Fatalf("Tracks: %v", err)
	}
	if len(tracks) != 2 {
		t.Fatalf("tracks = %d, want 2", len(tracks))
	}
	if want := []int{0, 1}; !slices.Equal(offsets, want) {
		t.Errorf("offsets = %v, want %v", offsets, want)
	}
}

func TestEmptyContainersAreNotErrors(t *testing.T) {
	c, _ := newServer(t, `{"result":{"playlisttracks_loop":[]}}`)
	tracks, err := c.Tracks("42")
	if err != nil {
		t.Fatalf("Tracks() error = %v", err)
	}
	if len(tracks) != 0 {
		t.Errorf("got %d tracks, want 0", len(tracks))
	}

	// LMS omits the loop key entirely when a container is empty.
	c2, _ := newServer(t, `{"result":{"count":0}}`)
	tracks, err = c2.Tracks("42")
	if err != nil {
		t.Fatalf("Tracks() with omitted loop error = %v", err)
	}
	if len(tracks) != 0 {
		t.Errorf("got %d tracks, want 0", len(tracks))
	}
}

func TestFetchAllPaginatesToResponseCount(t *testing.T) {
	var offsets []int
	got, err := fetchAll(2, func(offset int) ([]int, int, error) {
		offsets = append(offsets, offset)
		switch offset {
		case 0:
			return []int{1, 2}, 5, nil
		case 2:
			return []int{3, 4}, 5, nil
		case 4:
			return []int{5}, 5, nil
		default:
			return nil, 5, nil
		}
	})
	if err != nil {
		t.Fatalf("fetchAll: %v", err)
	}
	if want := []int{1, 2, 3, 4, 5}; !slices.Equal(got, want) {
		t.Errorf("fetchAll() = %v, want %v", got, want)
	}
	if want := []int{0, 2, 4}; !slices.Equal(offsets, want) {
		t.Errorf("offsets = %v, want %v", offsets, want)
	}
}

func TestArtists(t *testing.T) {
	c, _ := newServer(t, `{"result":{"artists_loop":[
		{"id":11,"artist":"Portishead"},{"id":12,"artist":"Massive Attack"}]}}`)
	got, err := c.Artists()
	if err != nil {
		t.Fatalf("Artists() error = %v", err)
	}
	if len(got) != 2 || got[0].ID != "11" || got[0].Name != "Portishead" {
		t.Errorf("Artists() = %+v", got)
	}
}

func TestArtistAlbumsScopesToArtist(t *testing.T) {
	c, got := newServer(t, `{"result":{"albums_loop":[
		{"id":5,"album":"Dummy","artist":"Portishead","artist_id":11,"year":1994},
		{"id":6,"album":"Untitled","artist":"Portishead","artist_id":11}]}}`)
	albums, err := c.ArtistAlbums("11")
	if err != nil {
		t.Fatalf("ArtistAlbums() error = %v", err)
	}
	if !strings.Contains(cmdString(got.Command), "artist_id:11") {
		t.Errorf("command = %v, want it to scope to artist_id:11", got.Command)
	}
	if albums[0].Name != "Dummy" || albums[0].Year != 1994 || albums[0].ArtistID != "11" {
		t.Errorf("album[0] = %+v", albums[0])
	}
	// A missing year tag must not fail the row.
	if albums[1].Year != 0 || albums[1].Name != "Untitled" {
		t.Errorf("album[1] = %+v, want year 0", albums[1])
	}
}

func TestAlbumListPagination(t *testing.T) {
	c, got := newServer(t, `{"result":{"albums_loop":[]}}`)
	if _, err := c.AlbumList(SortByNew, 50, 25); err != nil {
		t.Fatalf("AlbumList() error = %v", err)
	}
	s := cmdString(got.Command)
	if !strings.Contains(s, "albums 50 25") {
		t.Errorf("command = %q, want offset 50 and size 25 passed through", s)
	}
	if !strings.Contains(s, "sort:new") {
		t.Errorf("command = %q, want sort:new", s)
	}
}

func TestAlbumListDefaultsSort(t *testing.T) {
	c, got := newServer(t, `{"result":{"albums_loop":[]}}`)
	if _, err := c.AlbumList("", 0, 10); err != nil {
		t.Fatalf("AlbumList() error = %v", err)
	}
	if !strings.Contains(cmdString(got.Command), "sort:"+SortByName) {
		t.Errorf("command = %v, want the default sort applied", got.Command)
	}
}

func TestAlbumSortTypesIncludeDefault(t *testing.T) {
	c := New("http://nas:9000", "", "")
	types := c.AlbumSortTypes()
	if len(types) == 0 {
		t.Fatal("AlbumSortTypes() is empty")
	}
	for _, s := range types {
		if s.ID == c.DefaultAlbumSort() {
			return
		}
	}
	t.Errorf("DefaultAlbumSort() = %q is not among AlbumSortTypes()", c.DefaultAlbumSort())
}

func TestAlbumSortTypesUseSupportedLMSValues(t *testing.T) {
	got := New("http://nas:9000", "", "").AlbumSortTypes()
	ids := make([]string, len(got))
	for i, sortType := range got {
		ids[i] = sortType.ID
	}
	if want := []string{SortByName, SortByNew}; !slices.Equal(ids, want) {
		t.Errorf("AlbumSortTypes() = %v, want %v", ids, want)
	}
}

func TestAlbumTracksRequestsAlbumOrder(t *testing.T) {
	c, got := newServer(t, `{"result":{"titles_loop":[
		{"id":1,"title":"A","disc":1,"tracknum":1},{"id":2,"title":"B","disc":1,"tracknum":2}]}}`)
	tracks, err := c.AlbumTracks("5")
	if err != nil {
		t.Fatalf("AlbumTracks() error = %v", err)
	}
	s := cmdString(got.Command)
	if !strings.Contains(s, "album_id:5") {
		t.Errorf("command = %q, want album_id:5", s)
	}
	if !strings.Contains(s, "sort:albumtrack") {
		t.Errorf("command = %q, want disc/track ordering requested from the server", s)
	}
	if len(tracks) != 2 || tracks[0].Title != "A" || tracks[1].Title != "B" {
		t.Errorf("AlbumTracks() = %+v", tracks)
	}
}

// --- 4. track mapping and streaming -----------------------------------------

func TestToTrackMapping(t *testing.T) {
	c := New("http://nas:9000", "", "")

	tests := []struct {
		name string
		in   song
		want playlist.Track
	}{
		{
			name: "fully tagged",
			in: song{
				ID: "77", Title: "Roads", Artist: "Portishead", Album: "Dummy",
				Genre: "Trip Hop", Year: 1994, TrackNum: 5, Duration: 302.44,
				URL: "file:///music/roads.flac",
			},
			want: playlist.Track{
				Path: TrackURIPrefix + "77", Title: "Roads", Artist: "Portishead",
				Album: "Dummy", Genre: "Trip Hop", Year: 1994, TrackNumber: 5,
				// 302.44s truncates rather than rounds.
				DurationSecs: 302, Stream: true,
			},
		},
		{
			name: "only required fields",
			in:   song{ID: "78", Title: "Untitled"},
			want: playlist.Track{
				Path: TrackURIPrefix + "78", Title: "Untitled", Stream: true,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := c.toTrack(tt.in)

			if got.Path != tt.want.Path {
				t.Errorf("Path = %q, want %q", got.Path, tt.want.Path)
			}
			if got.Title != tt.want.Title || got.Artist != tt.want.Artist || got.Album != tt.want.Album {
				t.Errorf("title/artist/album = %q/%q/%q", got.Title, got.Artist, got.Album)
			}
			if got.Genre != tt.want.Genre || got.Year != tt.want.Year || got.TrackNumber != tt.want.TrackNumber {
				t.Errorf("genre/year/tracknum = %q/%d/%d", got.Genre, got.Year, got.TrackNumber)
			}
			if got.DurationSecs != tt.want.DurationSecs {
				t.Errorf("DurationSecs = %d, want %d", got.DurationSecs, tt.want.DurationSecs)
			}
			if got.Stream != tt.want.Stream || got.Realtime || got.Feed {
				t.Errorf("stream flags = stream:%v realtime:%v feed:%v", got.Stream, got.Realtime, got.Feed)
			}
			if got.Meta(provider.MetaLyrionID) != tt.in.ID.String() {
				t.Errorf("provider meta = %q, want %q", got.Meta(provider.MetaLyrionID), tt.in.ID)
			}
		})
	}
}

func TestFlexScalarsAcceptStringsAndNumbers(t *testing.T) {
	// LMS sends the same field as a number in one response and a quoted string
	// in another, so decoding must accept both.
	var s song
	raw := `{"id":9,"title":"T","year":"1994","tracknum":3,"duration":"302.44"}`
	if err := json.Unmarshal([]byte(raw), &s); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if s.ID.String() != "9" || s.Year != 1994 || s.TrackNum != 3 || s.Duration != 302.44 {
		t.Errorf("decoded = %+v", s)
	}

	// Junk in one field degrades to zero rather than failing the whole row.
	var junk song
	if err := json.Unmarshal([]byte(`{"id":"x","title":"T","year":"n/a"}`), &junk); err != nil {
		t.Fatalf("Unmarshal with junk year: %v", err)
	}
	if junk.Title != "T" || junk.Year != 0 {
		t.Errorf("decoded = %+v, want title kept and year zeroed", junk)
	}
}

func TestStreamURL(t *testing.T) {
	if got := New("http://nas:9000/", "", "").StreamURL("77"); got != "http://nas:9000/music/77/download" {
		t.Errorf("StreamURL() = %q", got)
	}
	got := New("http://nas:9000", "bob", "pw").StreamURL("77")
	if got != "http://bob:pw@nas:9000/music/77/download" {
		t.Errorf("StreamURL() with credentials = %q", got)
	}
}

func TestIsStreamURL(t *testing.T) {
	accept := []string{
		"http://nas:9000/music/77/download",
		"http://bob:pw@nas:9000/music/77/download",
		"http://nas:9000/music/77/download.mp3",
	}
	for _, u := range accept {
		if !IsStreamURL(u) {
			t.Errorf("IsStreamURL(%q) = false, want true", u)
		}
	}
	reject := []string{
		"http://nas:4533/rest/stream?id=77",       // Subsonic
		"http://jelly:8096/Audio/77/universal",    // Jellyfin
		"http://nas:9000/music/current/cover.jpg", // LMS, but artwork
		"https://example.com/song.mp3",            // unrelated
		"http://nas:9000/download",                // outside /music/
		"http://nas:9000/music/77/download/extra", // trailing path
		"",
	}
	for _, u := range reject {
		if IsStreamURL(u) {
			t.Errorf("IsStreamURL(%q) = true, want false", u)
		}
	}
}

func TestStreamURLRoundTripsThroughIsStreamURL(t *testing.T) {
	for _, c := range []*Client{New("http://nas:9000", "", ""), New("http://nas:9000", "bob", "pw")} {
		if u := c.StreamURL("77"); !IsStreamURL(u) {
			t.Errorf("IsStreamURL(%q) = false for a URL this provider produced", u)
		}
	}
}

// --- credentials never reach a track ----------------------------------------

// cliamp persists Track.Path to resume state and to the play history, and LMS
// authenticates with the user's real password rather than a revocable token, so
// a credential in Path would be written to disk in plaintext.
func TestTrackPathCarriesNoCredentials(t *testing.T) {
	c := New("http://nas:9000", "bob", "hunter2")
	tr := c.toTrack(song{ID: "77", Title: "T", URL: "file:///music/a.mp3"})
	if strings.Contains(tr.Path, "hunter2") || strings.Contains(tr.Path, "bob") {
		t.Errorf("Path %q leaks credentials", tr.Path)
	}
	if strings.Contains(tr.Path, "nas:9000") {
		t.Errorf("Path %q embeds the server host; it should be a bare track URI", tr.Path)
	}
	if tr.Path != TrackURIPrefix+"77" {
		t.Errorf("Path = %q, want %q", tr.Path, TrackURIPrefix+"77")
	}
}

func TestResolveSource(t *testing.T) {
	c := New("http://nas:9000", "bob", "hunter2")

	got, segments, err := c.ResolveSource(TrackURIPrefix + "77")
	if err != nil {
		t.Fatalf("ResolveSource: %v", err)
	}
	if segments != nil {
		t.Errorf("segments = %v, want nil for a single progressive file", segments)
	}
	// Credentials appear only here, at play time, and are never persisted.
	if got != "http://bob:hunter2@nas:9000/music/77/download" {
		t.Errorf("resolved = %q", got)
	}
	if !IsStreamURL(got) {
		t.Errorf("IsStreamURL(%q) = false for a resolved URL", got)
	}

	for _, bad := range []string{"", "tidal://track/77", "lyrion://track/", "nonsense"} {
		if _, _, err := c.ResolveSource(bad); err == nil {
			t.Errorf("ResolveSource(%q) = nil error, want a rejection", bad)
		}
	}
}

// --- 5. search --------------------------------------------------------------

func TestSearchTracks(t *testing.T) {
	c, got := newServer(t, `{"result":{"titles_loop":[{"id":1,"title":"Roads","artist":"Portishead"}]}}`)
	tracks, err := c.SearchTracks(context.Background(), "roads", 25)
	if err != nil {
		t.Fatalf("SearchTracks() error = %v", err)
	}
	if len(tracks) != 1 || tracks[0].Title != "Roads" {
		t.Errorf("SearchTracks() = %+v", tracks)
	}
	s := cmdString(got.Command)
	if !strings.Contains(s, "search:roads") {
		t.Errorf("command = %q, want search:roads", s)
	}
	if !strings.Contains(s, "titles 0 25") {
		t.Errorf("command = %q, want the caller's limit passed to the server", s)
	}
}

func TestSearchTracksNoMatches(t *testing.T) {
	c, _ := newServer(t, `{"result":{"count":0}}`)
	tracks, err := c.SearchTracks(context.Background(), "zzzz", 25)
	if err != nil {
		t.Fatalf("SearchTracks() error = %v", err)
	}
	if len(tracks) != 0 {
		t.Errorf("got %d tracks, want 0", len(tracks))
	}
}

func TestSearchTracksHonoursCancellation(t *testing.T) {
	c, _ := newServer(t, `{"result":{"titles_loop":[]}}`)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := c.SearchTracks(ctx, "roads", 25); err == nil {
		t.Fatal("expected an error for a cancelled context")
	}
}

// --- capability interface conformance ---------------------------------------

func TestClientSatisfiesCapabilityInterfaces(t *testing.T) {
	var c any = New("http://nas:9000", "", "")
	if _, ok := c.(provider.Searcher); !ok {
		t.Error("Client does not implement provider.Searcher")
	}
	if _, ok := c.(provider.ArtistBrowser); !ok {
		t.Error("Client does not implement provider.ArtistBrowser")
	}
	if _, ok := c.(provider.AlbumBrowser); !ok {
		t.Error("Client does not implement provider.AlbumBrowser")
	}
	if _, ok := c.(provider.AlbumTrackLoader); !ok {
		t.Error("Client does not implement provider.AlbumTrackLoader")
	}
	// Deliberately NOT a PlaybackReporter: cliamp plays the file directly, so
	// there is no LMS player whose history could honestly be updated.
	if _, ok := c.(provider.PlaybackReporter); ok {
		t.Error("Client implements provider.PlaybackReporter; see design.md")
	}
}

// --- tag sets ---------------------------------------------------------------

// The tag letters are verified against Slim/Control/Queries.pm. LMS silently
// omits any field whose letter is missing, so a wrong tag set degrades to blank
// metadata rather than an error — these assertions are the only thing that
// would catch it.
func TestSongTagsCoverEveryMappedField(t *testing.T) {
	// %tagMap in Queries.pm: a artist, l album, d duration, t tracknum,
	// y year, g genre. id and title are set unconditionally by _songData.
	for _, tag := range []string{"a", "l", "d", "t", "y", "g", "u"} {
		if !strings.Contains(songTags, tag) {
			t.Errorf("songTags %q is missing %q", songTags, tag)
		}
	}
}

func TestAlbumTagsUseUppercaseArtistID(t *testing.T) {
	// The albums query does NOT use %tagMap: there uppercase S is artist_id
	// and lowercase s is a sort textkey — the reverse of the titles query.
	if !strings.Contains(albumTags, "S") {
		t.Errorf("albumTags %q is missing uppercase S (artist_id)", albumTags)
	}
	for _, tag := range []string{"l", "y", "a"} {
		if !strings.Contains(albumTags, tag) {
			t.Errorf("albumTags %q is missing %q", albumTags, tag)
		}
	}
}

func TestQueriesSendTheirTagSets(t *testing.T) {
	c, got := newServer(t, `{"result":{"titles_loop":[]}}`)
	if _, err := c.AlbumTracks("5"); err != nil {
		t.Fatalf("AlbumTracks() error = %v", err)
	}
	if !strings.Contains(cmdString(got.Command), "tags:"+songTags) {
		t.Errorf("command = %v, want tags:%s", got.Command, songTags)
	}

	c2, got2 := newServer(t, `{"result":{"albums_loop":[]}}`)
	if _, err := c2.ArtistAlbums("11"); err != nil {
		t.Fatalf("ArtistAlbums() error = %v", err)
	}
	if !strings.Contains(cmdString(got2.Command), "tags:"+albumTags) {
		t.Errorf("command = %v, want tags:%s", got2.Command, albumTags)
	}
}

// --- plugin-contributed tracks ----------------------------------------------

func TestPluginTracksAreMarkedUnplayable(t *testing.T) {
	c := New("http://nas:9000", "", "")
	tests := []struct {
		name           string
		url            string
		wantUnplayable bool
	}{
		{"local file", "file:///music/a.mp3", false},
		{"spotify plugin", "spotify:track:2ZNJrC7A08B2MvhLJ00G1V", true},
		{"other plugin scheme", "qobuz://12345.flac", true},
		{"scheme without slashes", "deezer:track:99", true},
		{"remote stream", "http://example.com/live.mp3", true},
		{"no url tag requested", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := c.toTrack(song{ID: "1", Title: "T", URL: tt.url})
			if got.Unplayable != tt.wantUnplayable {
				t.Errorf("Unplayable = %v, want %v for url %q", got.Unplayable, tt.wantUnplayable, tt.url)
			}
		})
	}
}

const mixedTracks = `{"result":{"titles_loop":[
	{"id":1,"title":"Local","url":"file:///music/a.mp3"},
	{"id":2,"title":"Plugin","url":"spotify:track:xyz"}]}}`

// By default a plugin track is a dead entry the server will never stream, so
// it is omitted rather than listed.
func TestPluginTracksHiddenByDefault(t *testing.T) {
	c, _ := newServer(t, mixedTracks)
	tracks, err := c.AlbumTracks("5")
	if err != nil {
		t.Fatalf("AlbumTracks() error = %v", err)
	}
	if len(tracks) != 1 {
		t.Fatalf("got %d tracks, want only the file-backed one", len(tracks))
	}
	if tracks[0].Title != "Local" {
		t.Errorf("kept %q, want the local track", tracks[0].Title)
	}
}

func TestShowUnplayableRevealsPluginTracks(t *testing.T) {
	c, _ := newServer(t, mixedTracks)
	c.showUnplayable = true
	tracks, err := c.AlbumTracks("5")
	if err != nil {
		t.Fatalf("AlbumTracks() error = %v", err)
	}
	if len(tracks) != 2 {
		t.Fatalf("got %d tracks, want both", len(tracks))
	}
	if tracks[0].Unplayable {
		t.Error("local track marked unplayable")
	}
	if !tracks[1].Unplayable {
		t.Error("plugin track not flagged when revealed")
	}
}

const mixedPlaylists = `{"result":{"playlists_loop":[
	{"id":1,"playlist":"Mine","url":"file:///music/mine.m3u"},
	{"id":2,"playlist":"Spotify: Hits","url":"spotify:playlist:abc"}]}}`

// A plugin-imported playlist holds only that plugin's tracks, so with those
// hidden it would open empty. Hide the playlist itself instead.
func TestPluginPlaylistsHiddenByDefault(t *testing.T) {
	c, got := newServer(t, mixedPlaylists)
	pls, err := c.Playlists()
	if err != nil {
		t.Fatalf("Playlists() error = %v", err)
	}
	if !strings.Contains(cmdString(got.Command), "tags:u") {
		t.Errorf("command = %v, want the url tag requested for filtering", got.Command)
	}
	if len(pls) != 1 || pls[0].Name != "Mine" {
		t.Fatalf("got %+v, want only the local playlist", pls)
	}
}

func TestShowUnplayableRevealsPluginPlaylists(t *testing.T) {
	c, _ := newServer(t, mixedPlaylists)
	c.showUnplayable = true
	pls, err := c.Playlists()
	if err != nil {
		t.Fatalf("Playlists() error = %v", err)
	}
	if len(pls) != 2 {
		t.Fatalf("got %d playlists, want both", len(pls))
	}
}

// A server that returns no url tag must not vanish behind the filter.
func TestUntaggedResultsSurviveTheFilter(t *testing.T) {
	c, _ := newServer(t, `{"result":{"titles_loop":[{"id":1,"title":"NoURL"}]}}`)
	tracks, err := c.AlbumTracks("5")
	if err != nil {
		t.Fatalf("AlbumTracks() error = %v", err)
	}
	if len(tracks) != 1 {
		t.Errorf("got %d tracks, want the untagged track kept", len(tracks))
	}

	c2, _ := newServer(t, `{"result":{"playlists_loop":[{"id":1,"playlist":"NoURL"}]}}`)
	pls, err := c2.Playlists()
	if err != nil {
		t.Fatalf("Playlists() error = %v", err)
	}
	if len(pls) != 1 {
		t.Errorf("got %d playlists, want the untagged playlist kept", len(pls))
	}
}
