package qobuz

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand/v2"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/bjarneo/cliamp/playlist"
)

func TestTrackArtist(t *testing.T) {
	withPerformer := apiTrack{Performer: apiArtist{Name: "Performer"}}
	if got := trackArtist(withPerformer, nil); got != "Performer" {
		t.Errorf("performer name: got %q want %q", got, "Performer")
	}

	album := &apiAlbum{Artist: apiArtist{Name: "AlbumArtist"}}
	if got := trackArtist(apiTrack{}, album); got != "AlbumArtist" {
		t.Errorf("album fallback: got %q want %q", got, "AlbumArtist")
	}

	if got := trackArtist(apiTrack{}, nil); got != "" {
		t.Errorf("no artist: got %q want empty", got)
	}
}

func TestDedupeTracksByID(t *testing.T) {
	tracks := []apiTrack{
		{ID: "1", Title: "first"},
		{ID: "2", Title: "second"},
		{ID: "1", Title: "dup of first"},
		{ID: "3", Title: "third"},
		{ID: "2", Title: "dup of second"},
		{ID: "", Title: "no id a"},
		{ID: "", Title: "no id b"},
	}

	got := dedupeTracksByID(tracks)

	want := []struct {
		id    string
		title string
	}{
		{"1", "first"}, // first occurrence wins
		{"2", "second"},
		{"3", "third"},
		{"", "no id a"}, // empty-ID tracks are always kept
		{"", "no id b"},
	}
	if len(got) != len(want) {
		t.Fatalf("got %d tracks, want %d", len(got), len(want))
	}
	for i, w := range want {
		if got[i].ID.String() != w.id || got[i].Title != w.title {
			t.Errorf("track %d = {%q, %q}, want {%q, %q}",
				i, got[i].ID.String(), got[i].Title, w.id, w.title)
		}
	}
}

func TestDedupeTracksByIDEmpty(t *testing.T) {
	if got := dedupeTracksByID(nil); len(got) != 0 {
		t.Errorf("dedupeTracksByID(nil) = %v, want empty", got)
	}
}

func TestSampleTracks(t *testing.T) {
	mk := func(n int) []apiTrack {
		ts := make([]apiTrack, n)
		for i := range ts {
			ts[i] = apiTrack{ID: json.Number(strconv.Itoa(i))}
		}
		return ts
	}
	idSet := func(ts []apiTrack) map[string]bool {
		m := make(map[string]bool, len(ts))
		for _, tr := range ts {
			m[tr.ID.String()] = true
		}
		return m
	}

	r := rand.New(rand.NewPCG(42, 1024))

	// Under the cap: every track is kept, but the list must still be shuffled.
	// This is the case that used to be returned in playlist order unchanged.
	in := mk(100)
	got := sampleTracks(in, 500, r.Shuffle)
	if len(got) != 100 {
		t.Fatalf("under cap: len = %d, want 100", len(got))
	}
	want := idSet(in)
	for _, tr := range got {
		if !want[tr.ID.String()] {
			t.Errorf("under cap: track %s not from input", tr.ID)
		}
	}
	sameOrder := true
	for i := range got {
		if got[i].ID != in[i].ID {
			sameOrder = false
			break
		}
	}
	if sameOrder {
		t.Error("under cap: list was not shuffled")
	}

	// Over the cap: exactly n tracks, all from the input, no duplicates.
	big := mk(1000)
	all := idSet(big)
	for range 20 {
		s := sampleTracks(big, 10, r.Shuffle)
		if len(s) != 10 {
			t.Fatalf("over cap: len = %d, want 10", len(s))
		}
		seen := make(map[string]bool, len(s))
		for _, tr := range s {
			id := tr.ID.String()
			if !all[id] {
				t.Fatalf("over cap: track %q not from input", id)
			}
			if seen[id] {
				t.Fatalf("over cap: duplicate track %q", id)
			}
			seen[id] = true
		}
	}
}

func TestNewQualityNormalization(t *testing.T) {
	for _, q := range []int{5, 6, 7, 27} {
		if got := New(q).quality; got != q {
			t.Errorf("New(%d).quality = %d, want %d", q, got, q)
		}
	}
	for _, q := range []int{0, 1, 99} {
		if got := New(q).quality; got != defaultQuality {
			t.Errorf("New(%d).quality = %d, want default %d", q, got, defaultQuality)
		}
	}
}

// providerCalls runs each QobuzProvider method that talks to the API.
var providerCalls = []struct {
	name string
	call func(*QobuzProvider) error
}{
	{"Playlists", func(p *QobuzProvider) error { _, err := p.Playlists(); return err }},
	{"Tracks", func(p *QobuzProvider) error { _, err := p.Tracks("123"); return err }},
	{"Tracks favorites", func(p *QobuzProvider) error { _, err := p.Tracks(favoriteTracksID); return err }},
	{"Tracks random", func(p *QobuzProvider) error { _, err := p.Tracks(randomTracksID); return err }},
	{"SearchTracks", func(p *QobuzProvider) error {
		_, err := p.SearchTracks(context.Background(), "q", 5)
		return err
	}},
	{"Artists", func(p *QobuzProvider) error { _, err := p.Artists(); return err }},
	{"ArtistAlbums", func(p *QobuzProvider) error { _, err := p.ArtistAlbums("1"); return err }},
	{"AlbumList", func(p *QobuzProvider) error { _, err := p.AlbumList("favorites", 0, 10); return err }},
	{"AlbumTracks", func(p *QobuzProvider) error { _, err := p.AlbumTracks("1"); return err }},
}

func TestAPIErrorsMapToSignIn(t *testing.T) {
	tests := []struct {
		name       string
		status     int
		signIn     bool // a sign-in installs a new client while the request runs
		wantAuth   bool
		wantClient bool
	}{
		{name: "401 asks for sign-in", status: http.StatusUnauthorized, wantAuth: true},
		{name: "500 keeps the client", status: http.StatusInternalServerError, wantClient: true},
		{name: "401 keeps a newer client", status: http.StatusUnauthorized, signIn: true, wantAuth: true, wantClient: true},
	}
	for _, tt := range tests {
		for _, pc := range providerCalls {
			t.Run(tt.name+"/"+pc.name, func(t *testing.T) {
				t.Setenv("CLIAMP_CONFIG_DIR", t.TempDir())
				p := New(6)
				fresh := newClient("app", nil)
				srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if tt.signIn {
						p.mu.Lock()
						p.client = fresh
						p.mu.Unlock()
					}
					http.Error(w, `{"status":"error"}`, tt.status)
				}))
				defer srv.Close()

				p.client = testClient(srv)
				err := pc.call(p)
				if err == nil {
					t.Fatal("error = nil, want an error")
				}
				if got := errors.Is(err, playlist.ErrNeedsAuth); got != tt.wantAuth {
					t.Errorf("errors.Is(%v, ErrNeedsAuth) = %v, want %v", err, got, tt.wantAuth)
				}
				p.mu.Lock()
				got := p.client
				p.mu.Unlock()
				if hasClient := got != nil; hasClient != tt.wantClient {
					t.Errorf("client kept = %v, want %v", hasClient, tt.wantClient)
				}
				if tt.signIn && got != fresh {
					t.Error("the failed request dropped the client of the newer sign-in")
				}
			})
		}
	}
}

// catalogServer answers the list endpoints with the same 3 tracks and a
// signed URL for track/getFileUrl. It counts the getFileUrl calls.
func catalogServer(t *testing.T, fileURLCalls *atomic.Int32) *httptest.Server {
	const tracks = `{"items":[
		{"id":1,"title":"One","streamable":true,"performer":{"name":"A"}},
		{"id":2,"title":"Two","streamable":true,"performer":{"name":"A"}},
		{"id":3,"title":"Three","streamable":false,"performer":{"name":"A"}}],"total":3}`
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/playlist/get":
			fmt.Fprintf(w, `{"id":10,"tracks":%s}`, tracks)
		case "/playlist/getUserPlaylists":
			fmt.Fprint(w, `{"playlists":{"items":[{"id":10,"name":"Mix"}],"total":1}}`)
		case "/favorite/getUserFavorites", "/track/search":
			fmt.Fprintf(w, `{"tracks":%s}`, tracks)
		case "/album/get":
			fmt.Fprintf(w, `{"id":"a1","title":"Album","tracks":%s}`, tracks)
		case "/track/getFileUrl":
			fileURLCalls.Add(1)
			fmt.Fprintf(w, `{"url":"https://cdn.example/%s.flac?sig=x"}`, r.URL.Query().Get("track_id"))
		default:
			t.Errorf("unexpected request %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
}

// Opening a list must not sign a stream URL for each track: the tracks carry
// qobuz:// URIs, and the URL resolves when a track starts.
func TestListsCarryTrackURIs(t *testing.T) {
	tests := []struct {
		name string
		load func(*QobuzProvider) ([]playlist.Track, error)
	}{
		{"Tracks", func(p *QobuzProvider) ([]playlist.Track, error) { return p.Tracks("10") }},
		{"Tracks favorites", func(p *QobuzProvider) ([]playlist.Track, error) { return p.Tracks(favoriteTracksID) }},
		{"Tracks random", func(p *QobuzProvider) ([]playlist.Track, error) { return p.Tracks(randomTracksID) }},
		{"SearchTracks", func(p *QobuzProvider) ([]playlist.Track, error) {
			return p.SearchTracks(context.Background(), "q", 5)
		}},
		{"AlbumTracks", func(p *QobuzProvider) ([]playlist.Track, error) { return p.AlbumTracks("a1") }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var fileURLCalls atomic.Int32
			srv := catalogServer(t, &fileURLCalls)
			defer srv.Close()

			p := New(6)
			p.client = testClient(srv)
			got, err := tt.load(p)
			if err != nil {
				t.Fatalf("load error = %v", err)
			}
			if n := fileURLCalls.Load(); n != 0 {
				t.Errorf("track/getFileUrl calls = %d, want 0", n)
			}
			if len(got) != 3 {
				t.Fatalf("got %d tracks, want 3", len(got))
			}
			for _, tr := range got {
				id := tr.ProviderMeta["qobuz.id"]
				if want := "qobuz://track/" + id; tr.Path != want {
					t.Errorf("track %s Path = %q, want %q", id, tr.Path, want)
				}
				if wantUnplayable := id == "3"; tr.Unplayable != wantUnplayable {
					t.Errorf("track %s Unplayable = %v, want %v", id, tr.Unplayable, wantUnplayable)
				}
			}
		})
	}
}

func TestTrackFromAPI(t *testing.T) {
	fallback := &apiAlbum{Title: "Fallback", ReleaseDateOriginal: "1999-05-01", Artist: apiArtist{Name: "Album Artist"}, Genre: apiGenre{Name: "Jazz"}}
	own := &apiAlbum{Title: "Own", ReleaseDateOriginal: "2021-01-02", Genre: apiGenre{Name: "Rock"}}
	tests := []struct {
		name           string
		track          apiTrack
		fallback       *apiAlbum
		wantPath       string
		wantArtist     string
		wantAlbum      string
		wantGenre      string
		wantYear       int
		wantUnplayable bool
	}{
		{
			name:       "streamable track",
			track:      apiTrack{ID: "42", Title: "Song", Streamable: true, Performer: apiArtist{Name: "Performer"}, Album: own},
			wantPath:   "qobuz://track/42",
			wantArtist: "Performer",
			wantAlbum:  "Own",
			wantGenre:  "Rock",
			wantYear:   2021,
		},
		{
			name:           "not streamable",
			track:          apiTrack{ID: "7", Title: "Blocked", Streamable: false},
			wantPath:       "qobuz://track/7",
			wantUnplayable: true,
		},
		{
			name:       "album fallback",
			track:      apiTrack{ID: "8", Title: "Nested", Streamable: true},
			fallback:   fallback,
			wantPath:   "qobuz://track/8",
			wantArtist: "Album Artist",
			wantAlbum:  "Fallback",
			wantGenre:  "Jazz",
			wantYear:   1999,
		},
		{
			name:      "own album wins over fallback",
			track:     apiTrack{ID: "9", Title: "Both", Streamable: true, Album: own},
			fallback:  fallback,
			wantPath:  "qobuz://track/9",
			wantAlbum: "Own",
			wantGenre: "Rock",
			wantYear:  2021,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tr := trackFromAPI(tt.track, tt.fallback)
			if tr.Path != tt.wantPath {
				t.Errorf("Path = %q, want %q", tr.Path, tt.wantPath)
			}
			if tr.Title != tt.track.Title || tr.Artist != tt.wantArtist || tr.Album != tt.wantAlbum ||
				tr.Genre != tt.wantGenre || tr.Year != tt.wantYear {
				t.Errorf("track = %+v", tr)
			}
			if !tr.Stream || tr.Unplayable != tt.wantUnplayable {
				t.Errorf("Stream = %v, Unplayable = %v, want true, %v", tr.Stream, tr.Unplayable, tt.wantUnplayable)
			}
			if got := tr.ProviderMeta["qobuz.id"]; got != tt.track.ID.String() {
				t.Errorf("qobuz.id = %q, want %q", got, tt.track.ID)
			}
		})
	}
}

func TestResolveSource(t *testing.T) {
	tests := []struct {
		name       string
		uri        string
		status     int    // answer status for track/getFileUrl, 0 means 200
		body       string // answer body for track/getFileUrl
		wantURL    string
		wantErr    string
		wantAuth   bool
		wantCalls  int32
		keepClient bool
	}{
		{
			name:       "signed URL",
			uri:        "qobuz://track/5966783",
			body:       `{"url":"https://streaming-qobuz.example/5966783.flac?sig=a"}`,
			wantURL:    "https://streaming-qobuz.example/5966783.flac?sig=a",
			wantCalls:  1,
			keepClient: true,
		},
		{
			name:       "empty URL",
			uri:        "qobuz://track/1",
			body:       `{"url":""}`,
			wantErr:    "no stream URL",
			wantCalls:  1,
			keepClient: true,
		},
		{
			name:       "API error",
			uri:        "qobuz://track/1",
			status:     http.StatusBadRequest,
			body:       `{"message":"Invalid Request Signature"}`,
			wantErr:    "HTTP 400",
			wantCalls:  1,
			keepClient: true,
		},
		{
			name:      "401 asks for sign-in",
			uri:       "qobuz://track/1",
			status:    http.StatusUnauthorized,
			body:      `{"message":"User authentication is required."}`,
			wantAuth:  true,
			wantCalls: 1,
		},
		{name: "no ID", uri: "qobuz://track/", wantErr: "invalid track URI", keepClient: true},
		{name: "other scheme", uri: "tidal://track/1", wantErr: "invalid track URI", keepClient: true},
		{name: "ID with a path", uri: "qobuz://track/1/2", wantErr: "invalid track URI", keepClient: true},
		{name: "ID with a query", uri: "qobuz://track/1?x=y", wantErr: "invalid track URI", keepClient: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("CLIAMP_CONFIG_DIR", t.TempDir())
			var calls atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				if r.URL.Path != "/track/getFileUrl" {
					t.Errorf("path = %q, want /track/getFileUrl", r.URL.Path)
				}
				if got := r.URL.Query().Get("format_id"); got != "27" {
					t.Errorf("format_id = %q, want 27", got)
				}
				if tt.status != 0 {
					w.WriteHeader(tt.status)
				}
				_, _ = w.Write([]byte(tt.body))
			}))
			defer srv.Close()

			p := New(27)
			p.client = testClient(srv)
			got, err := p.ResolveSource(tt.uri)
			switch {
			case tt.wantAuth:
				if !errors.Is(err, playlist.ErrNeedsAuth) {
					t.Errorf("error = %v, want playlist.ErrNeedsAuth", err)
				}
			case tt.wantErr != "":
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Errorf("error = %v, want it to contain %q", err, tt.wantErr)
				}
			case err != nil:
				t.Fatalf("ResolveSource() error = %v", err)
			}
			if got != tt.wantURL {
				t.Errorf("ResolveSource() = %q, want %q", got, tt.wantURL)
			}
			if n := calls.Load(); n != tt.wantCalls {
				t.Errorf("API calls = %d, want %d", n, tt.wantCalls)
			}
			p.mu.Lock()
			hasClient := p.client != nil
			p.mu.Unlock()
			if hasClient != tt.keepClient {
				t.Errorf("client kept = %v, want %v", hasClient, tt.keepClient)
			}
		})
	}
}

func TestResolveSourceWithoutCredentialsAsksForSignIn(t *testing.T) {
	t.Setenv("CLIAMP_CONFIG_DIR", t.TempDir())
	_, err := New(6).ResolveSource("qobuz://track/1")
	if !errors.Is(err, playlist.ErrNeedsAuth) {
		t.Fatalf("error = %v, want playlist.ErrNeedsAuth", err)
	}
}

// TestAuthenticateCancelsEarlierFlow runs three overlapping sign-ins. Each
// new call must cancel the one before it, also after an older call returns
// late, and Close must cancel the last one.
func TestAuthenticateCancelsEarlierFlow(t *testing.T) {
	type flow struct {
		ctx     context.Context
		release chan struct{}
	}
	started := make(chan flow)
	orig := signIn
	t.Cleanup(func() { signIn = orig })
	signIn = func(ctx context.Context) (*client, error) {
		f := flow{ctx, make(chan struct{})}
		started <- f
		<-f.release
		return nil, ctx.Err()
	}

	p := New(defaultQuality)
	errs := make(chan error, 3)
	var flows []flow
	// finish lets flow i return and checks that it ended as canceled.
	finish := func(i int) {
		t.Helper()
		close(flows[i].release)
		if err := <-errs; !errors.Is(err, context.Canceled) {
			t.Fatalf("sign-in %d error = %v, want context.Canceled", i+1, err)
		}
	}
	for i := range 3 {
		go func() { errs <- p.Authenticate() }()
		flows = append(flows, <-started)
		if i == 0 {
			continue
		}
		if flows[i-1].ctx.Err() == nil {
			t.Fatalf("sign-in %d did not cancel sign-in %d", i+1, i)
		}
		// The older call returns after the newer call took over.
		finish(i - 1)
	}
	p.Close()
	if flows[2].ctx.Err() == nil {
		t.Fatal("Close did not cancel the last sign-in")
	}
	finish(2)
}
