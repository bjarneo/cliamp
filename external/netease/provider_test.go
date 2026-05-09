package netease

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"cliamp/provider"
)

func TestPlaylistsIncludesAccountListsAndCharts(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/user/playlist" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		if got := r.URL.Query().Get("uid"); got != "42" {
			t.Fatalf("uid = %q, want 42", got)
		}
		w.Write([]byte(`{"code":200,"playlist":[
			{"id":10,"name":"Daily Picks","userId":42,"trackCount":12,"specialType":5},
			{"id":11,"name":"Road Trip","userId":42,"trackCount":8,"specialType":0},
			{"id":12,"name":"Saved Mix","userId":99,"trackCount":20,"specialType":0}
		]}`))
	}))
	defer srv.Close()

	p := newWithBase(Config{Enabled: true, UserID: "42"}, srv.URL)
	lists, err := p.Playlists()
	if err != nil {
		t.Fatalf("Playlists() error = %v", err)
	}
	if len(lists) != 7 {
		t.Fatalf("got %d playlists, want 7", len(lists))
	}
	if lists[0].ID != "user:10" || lists[0].Name != "Liked Songs" || lists[0].Section != "My Playlists" {
		t.Fatalf("liked playlist = %+v", lists[0])
	}
	if lists[2].Section != "Saved Playlists" {
		t.Fatalf("saved playlist section = %q", lists[2].Section)
	}
	if lists[3].ID != "chart:3778678" || lists[3].Section != "Charts" {
		t.Fatalf("first chart = %+v", lists[3])
	}
}

func TestTracksMapsSongs(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/playlist/detail" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		if got := r.URL.Query().Get("id"); got != "10" {
			t.Fatalf("id = %q, want 10", got)
		}
		w.Write([]byte(`{"code":200,"result":{"tracks":[
			{"id":100,"name":"First Track","duration":123456,"no":3,
			 "artists":[{"name":"Artist One"},{"name":"Artist Two"}],
			 "album":{"name":"Album One"}}
		]}}`))
	}))
	defer srv.Close()

	p := newWithBase(Config{Enabled: true}, srv.URL)
	tracks, err := p.Tracks("user:10")
	if err != nil {
		t.Fatalf("Tracks() error = %v", err)
	}
	if len(tracks) != 1 {
		t.Fatalf("got %d tracks, want 1", len(tracks))
	}
	tr := tracks[0]
	if tr.Path != "https://music.163.com/#/song?id=100" {
		t.Fatalf("Path = %q", tr.Path)
	}
	if tr.Artist != "Artist One, Artist Two" || tr.Album != "Album One" {
		t.Fatalf("metadata = artist %q album %q", tr.Artist, tr.Album)
	}
	if tr.DurationSecs != 124 {
		t.Fatalf("DurationSecs = %d, want 124", tr.DurationSecs)
	}
	if tr.Meta(provider.MetaNetEaseID) != "100" {
		t.Fatalf("MetaNetEaseID = %q", tr.Meta(provider.MetaNetEaseID))
	}
}

func TestSearchTracks(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/search/get/web" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		if got := r.URL.Query().Get("s"); got != "query" {
			t.Fatalf("search query = %q, want query", got)
		}
		if got := r.URL.Query().Get("limit"); got != "5" {
			t.Fatalf("limit = %q, want 5", got)
		}
		w.Write([]byte(`{"code":200,"result":{"songs":[
			{"id":200,"name":"Search Hit","duration":1000,
			 "artists":[{"name":"Artist"}],"album":{"name":"Album"}}
		]}}`))
	}))
	defer srv.Close()

	p := newWithBase(Config{Enabled: true}, srv.URL)
	tracks, err := p.SearchTracks(context.Background(), " query ", 5)
	if err != nil {
		t.Fatalf("SearchTracks() error = %v", err)
	}
	if len(tracks) != 1 || tracks[0].Title != "Search Hit" {
		t.Fatalf("tracks = %+v", tracks)
	}
}

func TestCookieHeaderFromNetscapeFileFiltersNetEaseCookies(t *testing.T) {
	path := t.TempDir() + "/cookies.txt"
	data := strings.Join([]string{
		"# Netscape HTTP Cookie File",
		".music.163.com\tTRUE\t/\tTRUE\t0\tMUSIC_U\tabc",
		"#HttpOnly_.163.com\tTRUE\t/\tTRUE\t0\t__csrf\tdef",
		".example.com\tTRUE\t/\tTRUE\t0\tOTHER\tignored",
		"",
	}, "\n")
	if err := osWriteFile(path, data); err != nil {
		t.Fatal(err)
	}
	header, err := cookieHeaderFromNetscapeFile(path)
	if err != nil {
		t.Fatalf("cookieHeaderFromNetscapeFile() error = %v", err)
	}
	if header != "MUSIC_U=abc; __csrf=def" {
		t.Fatalf("header = %q", header)
	}
}

func TestExtractBrowserCookieHeaderMissingYTDLPShowsInstallHint(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	_, err := extractBrowserCookieHeader(context.Background(), "chrome")
	if err == nil {
		t.Fatal("extractBrowserCookieHeader() error = nil, want missing yt-dlp error")
	}
	msg := err.Error()
	if !strings.HasPrefix(msg, "yt-dlp not found. Install with: ") {
		t.Fatalf("error = %q", msg)
	}
	if strings.TrimPrefix(msg, "yt-dlp not found. Install with: ") == "" {
		t.Fatalf("missing install hint in error = %q", msg)
	}
}

func TestLiveCheckLoginWithBrowser(t *testing.T) {
	browser := os.Getenv("CLIAMP_NETEASE_LIVE_BROWSER")
	if browser == "" {
		t.Skip("set CLIAMP_NETEASE_LIVE_BROWSER to run live browser-cookie check")
	}
	acc, err := CheckLogin(context.Background(), browser)
	if err != nil {
		t.Fatalf("CheckLogin() error = %v", err)
	}
	if acc.UserID == "" {
		t.Fatal("CheckLogin() returned empty user id")
	}
}

func osWriteFile(path, data string) error {
	return os.WriteFile(path, []byte(data), 0o644)
}
