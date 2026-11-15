package netease

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bjarneo/cliamp/internal/ytdlp"
	"github.com/bjarneo/cliamp/provider"
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

// TestTracksMapsSongs checks the metadata of a preview song in the v6 format
// and of a looked-up song in the song detail format.
func TestTracksMapsSongs(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v6/playlist/detail":
			if got := r.URL.Query().Get("id"); got != "10" {
				t.Errorf("id = %q, want 10", got)
			}
			if got := r.URL.Query().Get("n"); got != "100000" {
				t.Errorf("n = %q, want 100000", got)
			}
			w.Write([]byte(`{"code":200,"playlist":{
				"trackIds":[{"id":100},{"id":200}],
				"tracks":[{"id":100,"name":"First Track","dt":123456,"no":3,
				 "ar":[{"name":"Artist One"},{"name":"Artist Two"}],
				 "al":{"name":"Album One"}}]}}`))
		case "/api/song/detail":
			if got := r.URL.Query().Get("ids"); got != "[200]" {
				t.Errorf("ids = %q, want [200]", got)
			}
			w.Write([]byte(`{"code":200,"songs":[{"id":200,"name":"Second Track",
				"duration":1000,"no":4,"artists":[{"name":"Artist Three"}],
				"album":{"name":"Album Two"}}]}`))
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	p := newWithBase(Config{Enabled: true}, srv.URL)
	tracks, err := p.Tracks("user:10")
	if err != nil {
		t.Fatalf("Tracks() error = %v", err)
	}
	want := []struct {
		path, title, artist, album string
		number, secs               int
		id                         string
	}{
		{"https://music.163.com/#/song?id=100", "First Track", "Artist One, Artist Two", "Album One", 3, 124, "100"},
		{"https://music.163.com/#/song?id=200", "Second Track", "Artist Three", "Album Two", 4, 1, "200"},
	}
	if len(tracks) != len(want) {
		t.Fatalf("got %d tracks, want %d", len(tracks), len(want))
	}
	for i, w := range want {
		tr := tracks[i]
		if tr.Path != w.path || tr.Title != w.title || tr.Artist != w.artist || tr.Album != w.album {
			t.Errorf("track %d = path %q title %q artist %q album %q", i, tr.Path, tr.Title, tr.Artist, tr.Album)
		}
		if tr.TrackNumber != w.number || tr.DurationSecs != w.secs {
			t.Errorf("track %d = number %d secs %d, want %d %d", i, tr.TrackNumber, tr.DurationSecs, w.number, w.secs)
		}
		if got := tr.Meta(provider.MetaNetEaseID); got != w.id {
			t.Errorf("track %d MetaNetEaseID = %q, want %q", i, got, w.id)
		}
		if !tr.Stream {
			t.Errorf("track %d Stream = false", i)
		}
	}
}

// fakePlaylist serves /api/v6/playlist/detail and /api/song/detail for one
// playlist. The detail endpoint returns all of ids and the first preview
// songs. The song lookup returns its songs in reverse order and leaves out
// the IDs in unknown.
type fakePlaylist struct {
	t       *testing.T
	ids     []int64
	preview int
	unknown map[int64]bool

	mu      sync.Mutex
	lookups [][]int64
}

func (f *fakePlaylist) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Path {
	case "/api/v6/playlist/detail":
		var resp struct {
			Code     int `json:"code"`
			Playlist struct {
				Tracks   []map[string]any `json:"tracks"`
				TrackIDs []map[string]any `json:"trackIds"`
			} `json:"playlist"`
		}
		resp.Code = 200
		resp.Playlist.Tracks = []map[string]any{}
		resp.Playlist.TrackIDs = []map[string]any{}
		for i, id := range f.ids {
			resp.Playlist.TrackIDs = append(resp.Playlist.TrackIDs, map[string]any{"id": id})
			if i < f.preview {
				resp.Playlist.Tracks = append(resp.Playlist.Tracks, map[string]any{
					"id": id, "name": fmt.Sprintf("Song %d", id), "dt": 1000,
					"ar": []map[string]string{{"name": "Artist"}}, "al": map[string]string{"name": "Album"},
				})
			}
		}
		_ = json.NewEncoder(w).Encode(resp)
	case "/api/song/detail":
		var ids []int64
		if err := json.Unmarshal([]byte(r.URL.Query().Get("ids")), &ids); err != nil {
			f.t.Errorf("ids = %q: %v", r.URL.Query().Get("ids"), err)
		}
		f.mu.Lock()
		f.lookups = append(f.lookups, ids)
		f.mu.Unlock()
		songs := []map[string]any{}
		for _, id := range slices.Backward(ids) {
			if f.unknown[id] {
				continue
			}
			songs = append(songs, map[string]any{
				"id": id, "name": fmt.Sprintf("Song %d", id), "duration": 1000,
				"artists": []map[string]string{{"name": "Artist"}}, "album": map[string]string{"name": "Album"},
			})
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"code": 200, "songs": songs})
	default:
		f.t.Errorf("unexpected path %s", r.URL.Path)
		http.NotFound(w, r)
	}
}

func seqIDs(n int) []int64 {
	ids := make([]int64, n)
	for i := range ids {
		ids[i] = int64(1000 + i)
	}
	return ids
}

func TestTracksLoadsFullPlaylist(t *testing.T) {
	tests := []struct {
		name    string
		ids     []int64
		preview int
		unknown map[int64]bool
		want    []int64
		lookups []int
	}{
		{name: "empty", ids: nil, want: nil},
		{name: "preview covers all", ids: seqIDs(20), preview: 20, want: seqIDs(20)},
		{name: "797 tracks with a 20 track preview", ids: seqIDs(797), preview: 20, want: seqIDs(797),
			lookups: []int{100, 100, 100, 100, 100, 100, 100, 77}},
		{name: "no preview", ids: seqIDs(5), want: seqIDs(5), lookups: []int{5}},
		{name: "duplicate ids keep each position", ids: []int64{1, 2, 3, 2, 1, 4, 4}, preview: 1,
			want: []int64{1, 2, 3, 2, 1, 4, 4}, lookups: []int{3}},
		{name: "unknown song is left out", ids: []int64{1, 2, 3}, unknown: map[int64]bool{2: true},
			want: []int64{1, 3}, lookups: []int{3}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fake := &fakePlaylist{t: t, ids: tt.ids, preview: tt.preview, unknown: tt.unknown}
			srv := httptest.NewServer(fake)
			defer srv.Close()

			p := newWithBase(Config{Enabled: true}, srv.URL)
			tracks, err := p.Tracks("user:10")
			if err != nil {
				t.Fatalf("Tracks() error = %v", err)
			}
			got := make([]int64, 0, len(tracks))
			for _, tr := range tracks {
				id, err := strconv.ParseInt(tr.Meta(provider.MetaNetEaseID), 10, 64)
				if err != nil {
					t.Fatalf("MetaNetEaseID = %q", tr.Meta(provider.MetaNetEaseID))
				}
				if want := fmt.Sprintf("Song %d", id); tr.Title != want {
					t.Errorf("track %d title = %q, want %q", id, tr.Title, want)
				}
				got = append(got, id)
			}
			if !slices.Equal(got, tt.want) {
				t.Fatalf("got %d tracks %v, want %d %v", len(got), got, len(tt.want), tt.want)
			}

			fake.mu.Lock()
			defer fake.mu.Unlock()
			sizes := make([]int, 0, len(fake.lookups))
			seen := map[int64]bool{}
			for _, ids := range fake.lookups {
				sizes = append(sizes, len(ids))
				for _, id := range ids {
					if seen[id] {
						t.Errorf("song %d looked up twice", id)
					}
					seen[id] = true
				}
			}
			if !slices.Equal(sizes, tt.lookups) {
				t.Fatalf("lookup sizes = %v, want %v", sizes, tt.lookups)
			}
		})
	}
}

func TestTracksFallsBackToPreviewWithoutTrackIDs(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v6/playlist/detail" {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		w.Write([]byte(`{"code":200,"playlist":{"tracks":[{"id":7,"name":"Only"},{"id":8,"name":"Preview"}]}}`))
	}))
	defer srv.Close()

	p := newWithBase(Config{Enabled: true}, srv.URL)
	tracks, err := p.Tracks("chart:3778678")
	if err != nil {
		t.Fatalf("Tracks() error = %v", err)
	}
	if len(tracks) != 2 || tracks[0].Title != "Only" || tracks[1].Title != "Preview" {
		t.Fatalf("tracks = %+v", tracks)
	}
}

// TestTracksFailsOnPartialLoad checks that a failed request fails the load,
// so the queue never gets only a part of the playlist.
func TestTracksFailsOnPartialLoad(t *testing.T) {
	tests := []struct {
		name string
		// detail answers /api/v6/playlist/detail. Nil serves the fake playlist.
		detail http.HandlerFunc
		// lookup answers song detail request n, which starts at 1. Nil serves
		// the fake songs.
		lookup  func(w http.ResponseWriter, n int32)
		wantErr string
	}{
		{
			name:    "playlist detail code",
			detail:  func(w http.ResponseWriter, _ *http.Request) { w.Write([]byte(`{"code":404}`)) },
			wantErr: "playlist detail failed with code 404",
		},
		{
			name:    "playlist detail http status",
			detail:  func(w http.ResponseWriter, _ *http.Request) { http.Error(w, "down", http.StatusBadGateway) },
			wantErr: "http status 502",
		},
		{
			name: "second song detail batch http status",
			lookup: func(w http.ResponseWriter, n int32) {
				if n == 2 {
					http.Error(w, "down", http.StatusInternalServerError)
					return
				}
				w.Write([]byte(`{"code":200,"songs":[]}`))
			},
			wantErr: "song detail: netease: http status 500",
		},
		{
			name:    "song detail code",
			lookup:  func(w http.ResponseWriter, _ int32) { w.Write([]byte(`{"code":-460,"songs":[]}`)) },
			wantErr: "song detail failed with code -460",
		},
		{
			name:    "song detail bad json",
			lookup:  func(w http.ResponseWriter, _ int32) { w.Write([]byte(`{"code":200,"songs":[`)) },
			wantErr: "song detail: netease: decode response",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			full := &fakePlaylist{t: t, ids: seqIDs(250), preview: 20}
			var lookups atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch {
				case r.URL.Path == "/api/v6/playlist/detail" && tt.detail != nil:
					tt.detail(w, r)
				case r.URL.Path == "/api/song/detail" && tt.lookup != nil:
					tt.lookup(w, lookups.Add(1))
				default:
					full.ServeHTTP(w, r)
				}
			}))
			defer srv.Close()

			p := newWithBase(Config{Enabled: true}, srv.URL)
			tracks, err := p.Tracks("user:10")
			if err == nil {
				t.Fatalf("Tracks() = %d tracks, want error", len(tracks))
			}
			if tracks != nil {
				t.Errorf("Tracks() = %d tracks with the error, want none", len(tracks))
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("error = %q, want it to contain %q", err, tt.wantErr)
			}
		})
	}
}

// TestPlaylistSongsStopsWhenCancelled checks that a cancel during the second
// song detail batch ends the load and sends no more batches.
func TestPlaylistSongsStopsWhenCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	full := &fakePlaylist{t: t, ids: seqIDs(500)}
	var lookups atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/song/detail" && lookups.Add(1) == 2 {
			cancel()
			// Hold the response until the client drops the request.
			select {
			case <-r.Context().Done():
			case <-time.After(5 * time.Second):
				t.Error("client did not drop the request after the cancel")
			}
			return
		}
		full.ServeHTTP(w, r)
	}))
	defer srv.Close()

	p := newWithBase(Config{Enabled: true}, srv.URL)
	songs, err := p.playlistSongs(ctx, "10")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("playlistSongs() = %d songs, error %v, want context.Canceled", len(songs), err)
	}
	if songs != nil {
		t.Errorf("playlistSongs() = %d songs with the error, want none", len(songs))
	}
	if got := lookups.Load(); got != 2 {
		t.Fatalf("song detail requests = %d, want 2", got)
	}
}

func TestIDList(t *testing.T) {
	tests := []struct {
		ids  []int64
		want string
	}{
		{nil, "[]"},
		{[]int64{5}, "[5]"},
		{[]int64{2727606675, 1, 42}, "[2727606675,1,42]"},
	}
	for _, tt := range tests {
		if got := idList(tt.ids); got != tt.want {
			t.Errorf("idList(%v) = %q, want %q", tt.ids, got, tt.want)
		}
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

func TestCookieHeaderFromNetscapeFileSkipsExpiredCookies(t *testing.T) {
	path := t.TempDir() + "/cookies.txt"
	data := strings.Join([]string{
		".music.163.com\tTRUE\t/\tTRUE\t1\tMUSIC_U\tstale",
		".music.163.com\tTRUE\t/\tTRUE\t4102444800\tMUSIC_U\tfresh",
		".163.com\tTRUE\t/\tTRUE\t0\t__csrf\tsession",
		".163.com\tTRUE\t/\tTRUE\t0\t__csrf\tsecond",
		"",
	}, "\n")
	if err := osWriteFile(path, data); err != nil {
		t.Fatal(err)
	}
	header, err := cookieHeaderFromNetscapeFile(path)
	if err != nil {
		t.Fatalf("cookieHeaderFromNetscapeFile() error = %v", err)
	}
	if want := "MUSIC_U=fresh; __csrf=session"; header != want {
		t.Fatalf("header = %q, want %q", header, want)
	}
}

func TestAccountReadsCookiesFileWithoutYTDLP(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	path := t.TempDir() + "/cookies.txt"
	if err := osWriteFile(path, ".music.163.com\tTRUE\t/\tTRUE\t0\tMUSIC_U\tabc\n"); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Cookie"); got != "MUSIC_U=abc" {
			t.Errorf("Cookie = %q, want MUSIC_U=abc", got)
		}
		w.Write([]byte(`{"code":200,"account":{"id":42}}`))
	}))
	defer srv.Close()

	p := newWithBase(Config{Enabled: true, CookiesFrom: "chrome", CookiesFile: path}, srv.URL)
	acc, err := p.Account(context.Background())
	if err != nil {
		t.Fatalf("Account() error = %v", err)
	}
	if acc.UserID != "42" {
		t.Fatalf("UserID = %q, want 42", acc.UserID)
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
	if got, want := strings.TrimPrefix(msg, "yt-dlp not found. Install with: "), ytdlp.InstallHint(); got != want {
		t.Fatalf("install hint = %q, want %q", got, want)
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
