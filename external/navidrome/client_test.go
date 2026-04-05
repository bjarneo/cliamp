package navidrome

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"cliamp/config"
	"cliamp/playlist"
	"cliamp/provider"
)

// subsonicHandler serves fake Subsonic API responses for testing.
func subsonicHandler(t *testing.T) http.HandlerFunc {
	t.Helper()
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		path := r.URL.Path
		switch {
		case strings.HasSuffix(path, "/rest/getPlaylists"):
			w.Write([]byte(`{
				"subsonic-response": {
					"status": "ok",
					"playlists": {
						"playlist": [
							{"id":"pl-1","name":"Jazz Classics","songCount":12},
							{"id":"pl-2","name":"Late Night","songCount":8}
						]
					}
				}
			}`))
		case strings.HasSuffix(path, "/rest/getPlaylist"):
			w.Write([]byte(`{
				"subsonic-response": {
					"status": "ok",
					"playlist": {
						"entry": [
							{
								"id":"song-1","title":"So What","artist":"Miles Davis",
								"album":"Kind of Blue","year":1959,"track":1,"genre":"Jazz","duration":565
							},
							{
								"id":"song-2","title":"Blue in Green","artist":"Miles Davis",
								"album":"Kind of Blue","year":1959,"track":3,"genre":"Jazz","duration":327
							}
						]
					}
				}
			}`))
		case strings.HasSuffix(path, "/rest/getArtists"):
			w.Write([]byte(`{
				"subsonic-response": {
					"status": "ok",
					"artists": {
						"index": [
							{
								"artist": [
									{"id":"ar-1","name":"Miles Davis","albumCount":5},
									{"id":"ar-2","name":"Mingus","albumCount":3}
								]
							},
							{
								"artist": [
									{"id":"ar-3","name":"Thelonious Monk","albumCount":4}
								]
							}
						]
					}
				}
			}`))
		case strings.HasSuffix(path, "/rest/getArtist"):
			w.Write([]byte(`{
				"subsonic-response": {
					"status": "ok",
					"artist": {
						"album": [
							{"id":"al-1","name":"Kind of Blue","artist":"Miles Davis","artistId":"ar-1","year":1959,"songCount":5,"genre":"Jazz"},
							{"id":"al-2","name":"Bitches Brew","artist":"Miles Davis","artistId":"ar-1","year":1970,"songCount":4,"genre":"Jazz"}
						]
					}
				}
			}`))
		case strings.HasSuffix(path, "/rest/getAlbumList2"):
			w.Write([]byte(`{
				"subsonic-response": {
					"status": "ok",
					"albumList2": {
						"album": [
							{"id":"al-1","name":"Kind of Blue","artist":"Miles Davis","artistId":"ar-1","year":1959,"songCount":5,"genre":"Jazz"}
						]
					}
				}
			}`))
		case strings.HasSuffix(path, "/rest/getAlbum"):
			w.Write([]byte(`{
				"subsonic-response": {
					"status": "ok",
					"album": {
						"song": [
							{
								"id":"song-1","title":"So What","artist":"Miles Davis",
								"album":"Kind of Blue","year":1959,"track":1,"genre":"Jazz","duration":565
							}
						]
					}
				}
			}`))
		case strings.HasSuffix(path, "/rest/scrobble"):
			w.Write([]byte(`{"subsonic-response":{"status":"ok"}}`))
		default:
			t.Errorf("unexpected path: %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}
}

func newTestClient(t *testing.T) (*NavidromeClient, *httptest.Server) {
	t.Helper()
	srv := httptest.NewServer(subsonicHandler(t))
	c := New(srv.URL, "testuser", "testpass")
	return c, srv
}

func TestName(t *testing.T) {
	c := New("http://localhost", "u", "p")
	if c.Name() != "Navidrome" {
		t.Errorf("Name() = %q, want %q", c.Name(), "Navidrome")
	}
}

func TestPlaylists(t *testing.T) {
	c, srv := newTestClient(t)
	defer srv.Close()

	lists, err := c.Playlists()
	if err != nil {
		t.Fatalf("Playlists() error: %v", err)
	}
	if len(lists) != 2 {
		t.Fatalf("expected 2 playlists, got %d", len(lists))
	}
	if lists[0].ID != "pl-1" || lists[0].Name != "Jazz Classics" || lists[0].TrackCount != 12 {
		t.Errorf("lists[0] = %+v", lists[0])
	}
	if lists[1].ID != "pl-2" || lists[1].Name != "Late Night" || lists[1].TrackCount != 8 {
		t.Errorf("lists[1] = %+v", lists[1])
	}
}

func TestPlaylists_Cached(t *testing.T) {
	callCount := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/rest/getPlaylists") {
			callCount++
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{
			"subsonic-response": {
				"status": "ok",
				"playlists": {"playlist": [{"id":"1","name":"P","songCount":1}]}
			}
		}`))
	}))
	defer srv.Close()

	c := New(srv.URL, "u", "p")
	if _, err := c.Playlists(); err != nil {
		t.Fatalf("first Playlists() error: %v", err)
	}
	if _, err := c.Playlists(); err != nil {
		t.Fatalf("second Playlists() error: %v", err)
	}
	if callCount != 1 {
		t.Errorf("expected getPlaylists called once, called %d times", callCount)
	}
}

func TestTracks(t *testing.T) {
	c, srv := newTestClient(t)
	defer srv.Close()

	tracks, err := c.Tracks("pl-1")
	if err != nil {
		t.Fatalf("Tracks() error: %v", err)
	}
	if len(tracks) != 2 {
		t.Fatalf("expected 2 tracks, got %d", len(tracks))
	}

	tr := tracks[0]
	if tr.Title != "So What" {
		t.Errorf("Title = %q, want %q", tr.Title, "So What")
	}
	if tr.Artist != "Miles Davis" {
		t.Errorf("Artist = %q, want %q", tr.Artist, "Miles Davis")
	}
	if tr.Album != "Kind of Blue" {
		t.Errorf("Album = %q, want %q", tr.Album, "Kind of Blue")
	}
	if tr.Year != 1959 {
		t.Errorf("Year = %d, want 1959", tr.Year)
	}
	if tr.TrackNumber != 1 {
		t.Errorf("TrackNumber = %d, want 1", tr.TrackNumber)
	}
	if tr.Genre != "Jazz" {
		t.Errorf("Genre = %q, want %q", tr.Genre, "Jazz")
	}
	if tr.DurationSecs != 565 {
		t.Errorf("DurationSecs = %d, want 565", tr.DurationSecs)
	}
	if !tr.Stream {
		t.Error("Stream = false, want true")
	}
	if got := tr.Meta(provider.MetaNavidromeID); got != "song-1" {
		t.Errorf("Meta(NavidromeID) = %q, want %q", got, "song-1")
	}
	if !strings.Contains(tr.Path, "/rest/stream") {
		t.Errorf("Path %q missing /rest/stream", tr.Path)
	}
}

func TestTracks_Cached(t *testing.T) {
	callCount := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/rest/getPlaylist") {
			callCount++
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{
			"subsonic-response": {
				"status": "ok",
				"playlist": {"entry": [{"id":"1","title":"T","artist":"A","album":"Al","duration":60}]}
			}
		}`))
	}))
	defer srv.Close()

	c := New(srv.URL, "u", "p")
	if _, err := c.Tracks("pl-1"); err != nil {
		t.Fatalf("first Tracks() error: %v", err)
	}
	if _, err := c.Tracks("pl-1"); err != nil {
		t.Fatalf("second Tracks() error: %v", err)
	}
	if callCount != 1 {
		t.Errorf("expected getPlaylist called once, called %d times", callCount)
	}
}

func TestArtists(t *testing.T) {
	c, srv := newTestClient(t)
	defer srv.Close()

	artists, err := c.Artists()
	if err != nil {
		t.Fatalf("Artists() error: %v", err)
	}
	if len(artists) != 3 {
		t.Fatalf("expected 3 artists (across 2 indexes), got %d", len(artists))
	}
	if artists[0].ID != "ar-1" || artists[0].Name != "Miles Davis" || artists[0].AlbumCount != 5 {
		t.Errorf("artists[0] = %+v", artists[0])
	}
	if artists[2].ID != "ar-3" || artists[2].Name != "Thelonious Monk" {
		t.Errorf("artists[2] = %+v", artists[2])
	}
}

func TestArtistAlbums(t *testing.T) {
	c, srv := newTestClient(t)
	defer srv.Close()

	albums, err := c.ArtistAlbums("ar-1")
	if err != nil {
		t.Fatalf("ArtistAlbums() error: %v", err)
	}
	if len(albums) != 2 {
		t.Fatalf("expected 2 albums, got %d", len(albums))
	}
	if albums[0].ID != "al-1" || albums[0].Name != "Kind of Blue" || albums[0].Year != 1959 {
		t.Errorf("albums[0] = %+v", albums[0])
	}
	if albums[1].ID != "al-2" || albums[1].Name != "Bitches Brew" || albums[1].Year != 1970 {
		t.Errorf("albums[1] = %+v", albums[1])
	}
}

func TestAlbumList(t *testing.T) {
	c, srv := newTestClient(t)
	defer srv.Close()

	albums, err := c.AlbumList(SortAlphabeticalByName, 0, 50)
	if err != nil {
		t.Fatalf("AlbumList() error: %v", err)
	}
	if len(albums) != 1 {
		t.Fatalf("expected 1 album, got %d", len(albums))
	}
	if albums[0].ID != "al-1" || albums[0].Name != "Kind of Blue" {
		t.Errorf("albums[0] = %+v", albums[0])
	}
}

func TestAlbumTracks(t *testing.T) {
	c, srv := newTestClient(t)
	defer srv.Close()

	tracks, err := c.AlbumTracks("al-1")
	if err != nil {
		t.Fatalf("AlbumTracks() error: %v", err)
	}
	if len(tracks) != 1 {
		t.Fatalf("expected 1 track, got %d", len(tracks))
	}
	if tracks[0].Title != "So What" || tracks[0].DurationSecs != 565 {
		t.Errorf("tracks[0] = %+v", tracks[0])
	}
}

func TestSubsonicError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{
			"subsonic-response": {
				"status": "failed",
				"error": {"code": 40, "message": "Wrong username or password"}
			}
		}`))
	}))
	defer srv.Close()

	c := New(srv.URL, "bad", "creds")
	_, err := c.Playlists()
	if err == nil {
		t.Fatal("expected error for bad credentials, got nil")
	}
	if !strings.Contains(err.Error(), "Wrong username or password") {
		t.Errorf("error = %q, expected credential error", err.Error())
	}
}

func TestHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	c := New(srv.URL, "u", "p")
	_, err := c.Playlists()
	if err == nil {
		t.Fatal("expected error for HTTP 500, got nil")
	}
}

func TestBuildURL_AuthParams(t *testing.T) {
	c := New("https://music.example.com", "alice", "secret123")
	u := c.buildURL("ping", nil)

	parsed, err := url.Parse(u)
	if err != nil {
		t.Fatalf("buildURL() returned invalid URL: %v", err)
	}
	q := parsed.Query()
	if q.Get("u") != "alice" {
		t.Errorf("user = %q, want alice", q.Get("u"))
	}
	if q.Get("t") == "" {
		t.Error("token (t) is empty")
	}
	if q.Get("s") == "" {
		t.Error("salt (s) is empty")
	}
	if q.Get("v") != "1.0.0" {
		t.Errorf("version = %q, want 1.0.0", q.Get("v"))
	}
	if q.Get("c") != "cliamp" {
		t.Errorf("client = %q, want cliamp", q.Get("c"))
	}
	if q.Get("f") != "json" {
		t.Errorf("format = %q, want json", q.Get("f"))
	}
	if !strings.HasPrefix(u, "https://music.example.com/rest/ping?") {
		t.Errorf("URL = %q, missing expected prefix", u)
	}
}

func TestIsSubsonicStreamURL(t *testing.T) {
	tests := []struct {
		url  string
		want bool
	}{
		{"https://music.example.com/rest/stream?id=1", true},
		{"https://music.example.com/rest/stream.view?id=1", true},
		{"https://music.example.com/rest/download?id=1", true},
		{"https://music.example.com/rest/download.view?id=1", true},
		{"https://music.example.com/rest/getPlaylists", false},
		{"https://example.com/song.mp3", false},
		{"", false},
	}
	for _, tt := range tests {
		if got := IsSubsonicStreamURL(tt.url); got != tt.want {
			t.Errorf("IsSubsonicStreamURL(%q) = %v, want %v", tt.url, got, tt.want)
		}
	}
}

func TestNewFromEnv(t *testing.T) {
	t.Setenv("NAVIDROME_URL", "")
	t.Setenv("NAVIDROME_USER", "")
	t.Setenv("NAVIDROME_PASS", "")
	if c := NewFromEnv(); c != nil {
		t.Error("NewFromEnv() should return nil when env vars are empty")
	}

	t.Setenv("NAVIDROME_URL", "https://music.test")
	t.Setenv("NAVIDROME_USER", "alice")
	t.Setenv("NAVIDROME_PASS", "secret")
	c := NewFromEnv()
	if c == nil {
		t.Fatal("NewFromEnv() returned nil with all env vars set")
	}
	if c.url != "https://music.test" || c.user != "alice" || c.password != "secret" {
		t.Errorf("NewFromEnv() = %+v", c)
	}
}

func TestNewFromConfig(t *testing.T) {
	tests := []struct {
		name    string
		cfg     config.NavidromeConfig
		wantNil bool
	}{
		{"empty", config.NavidromeConfig{}, true},
		{"no password", config.NavidromeConfig{URL: "http://localhost", User: "u"}, true},
		{"no url", config.NavidromeConfig{User: "u", Password: "p"}, true},
		{"valid", config.NavidromeConfig{URL: "http://localhost", User: "u", Password: "p"}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := NewFromConfig(tt.cfg)
			if (c == nil) != tt.wantNil {
				t.Errorf("NewFromConfig(%+v) nil=%v, want nil=%v", tt.cfg, c == nil, tt.wantNil)
			}
		})
	}
}

func TestNewFromConfig_BrowseSort(t *testing.T) {
	cfg := config.NavidromeConfig{
		URL: "http://localhost", User: "u", Password: "p",
		BrowseSort: SortNewest,
	}
	c := NewFromConfig(cfg)
	if c.browseSort != SortNewest {
		t.Errorf("browseSort = %q, want %q", c.browseSort, SortNewest)
	}
}

func TestNewFromConfig_ScrobbleDisabled(t *testing.T) {
	cfg := config.NavidromeConfig{
		URL: "http://localhost", User: "u", Password: "p",
		ScrobbleDisabled: true,
	}
	c := NewFromConfig(cfg)
	if !c.scrobbleDisabled {
		t.Error("scrobbleDisabled = false, want true")
	}
}

func TestAlbumSortTypes(t *testing.T) {
	c := New("http://localhost", "u", "p")
	sorts := c.AlbumSortTypes()
	if len(sorts) != 8 {
		t.Fatalf("expected 8 sort types, got %d", len(sorts))
	}
}

func TestDefaultAlbumSort(t *testing.T) {
	c := New("http://localhost", "u", "p")
	if c.DefaultAlbumSort() != SortAlphabeticalByName {
		t.Errorf("DefaultAlbumSort() = %q, want %q", c.DefaultAlbumSort(), SortAlphabeticalByName)
	}
	c.browseSort = SortNewest
	if c.DefaultAlbumSort() != SortNewest {
		t.Errorf("DefaultAlbumSort() = %q, want %q", c.DefaultAlbumSort(), SortNewest)
	}
}

func TestCanReportPlayback(t *testing.T) {
	c := New("http://localhost", "u", "p")
	track := trackWithNavidromeMeta("song-1")
	if !c.CanReportPlayback(track) {
		t.Error("CanReportPlayback() = false for track with navidrome ID")
	}

	c.scrobbleDisabled = true
	if c.CanReportPlayback(track) {
		t.Error("CanReportPlayback() = true when scrobbling is disabled")
	}
}

func TestCanReportPlayback_NoMeta(t *testing.T) {
	c := New("http://localhost", "u", "p")
	track := trackWithNavidromeMeta("")
	if c.CanReportPlayback(track) {
		t.Error("CanReportPlayback() = true for track without navidrome ID")
	}
}

func TestScrobble(t *testing.T) {
	var gotSubmission string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/rest/scrobble") {
			gotSubmission = r.URL.Query().Get("submission")
		}
		w.Write([]byte(`{"subsonic-response":{"status":"ok"}}`))
	}))
	defer srv.Close()

	c := New(srv.URL, "u", "p")

	c.ReportNowPlaying(trackWithNavidromeMeta("song-1"), 0, false)
	if gotSubmission != "false" {
		t.Errorf("ReportNowPlaying submission = %q, want false", gotSubmission)
	}

	c.ReportScrobble(trackWithNavidromeMeta("song-1"), 0, 0, false)
	if gotSubmission != "true" {
		t.Errorf("ReportScrobble submission = %q, want true", gotSubmission)
	}
}

func TestCheckSubsonicError(t *testing.T) {
	if err := checkSubsonicError("ok", nil); err != nil {
		t.Errorf("expected nil for status ok, got %v", err)
	}
	if err := checkSubsonicError("", nil); err != nil {
		t.Errorf("expected nil for empty status, got %v", err)
	}

	err := checkSubsonicError("failed", &subsonicError{Code: 40, Message: "Wrong password"})
	if err == nil {
		t.Fatal("expected error for failed status")
	}
	if !strings.Contains(err.Error(), "Wrong password") {
		t.Errorf("error = %q, missing message", err.Error())
	}

	err = checkSubsonicError("failed", nil)
	if err == nil {
		t.Fatal("expected error for failed status with nil error body")
	}
}

func trackWithNavidromeMeta(id string) playlist.Track {
	meta := map[string]string{}
	if id != "" {
		meta[provider.MetaNavidromeID] = id
	}
	return playlist.Track{ProviderMeta: meta}
}
