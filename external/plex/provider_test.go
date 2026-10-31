package plex

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/bjarneo/cliamp/config"
	"github.com/bjarneo/cliamp/playlist"
	"github.com/bjarneo/cliamp/provider"
)

// sectionsHandler returns a handler that serves a single music section
// plus one audio playlist (and one video playlist that must be filtered out).
func sectionsHandler(t *testing.T) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasSuffix(r.URL.Path, "/playlists/24872/items"):
			w.Write([]byte(`{
				"MediaContainer": {
					"totalSize": 1,
					"Metadata": [
						{
							"ratingKey":"300","title":"Highway to Hell","grandparentTitle":"AC/DC",
							"parentTitle":"Highway to Hell","parentYear":1979,"index":1,"duration":208000,"userRating":10,
							"Media":[{"Part":[{"key":"/library/parts/3/333/HighwayToHell.flac"}]}]
						}
					]
				}
			}`))
		case strings.HasSuffix(r.URL.Path, "/playlists"):
			w.Write([]byte(`{
				"MediaContainer": {
					"Metadata": [
						{"ratingKey":"24872","title":"GOOD","smart":true,"playlistType":"audio","leafCount":3,"duration":600000},
						{"ratingKey":"9001","title":"Movies","smart":false,"playlistType":"video","leafCount":5,"duration":9000000}
					]
				}
			}`))
		case strings.HasSuffix(r.URL.Path, "/library/sections"):
			w.Write([]byte(`{"MediaContainer":{"Directory":[{"key":"3","type":"artist","title":"Music"}]}}`))
		case strings.HasSuffix(r.URL.Path, "/library/sections/3/all"):
			w.Write([]byte(`{
				"MediaContainer": {
					"totalSize": 2,
					"Metadata": [
						{"ratingKey":"100","title":"Kind of Blue","parentTitle":"Miles Davis","year":1959,"leafCount":5},
						{"ratingKey":"101","title":"Bitches Brew","parentTitle":"Miles Davis","year":1970,"leafCount":4}
					]
				}
			}`))
		case strings.Contains(r.URL.Path, "/library/metadata/100/children"):
			w.Write([]byte(`{
				"MediaContainer": {
					"Metadata": [
						{
							"ratingKey":"200","title":"So What","grandparentTitle":"Miles Davis",
							"parentTitle":"Kind of Blue","year":1959,"index":1,"duration":565000,
							"Media":[{"Part":[{"key":"/library/parts/1/111/SoWhat.flac"}]}]
						}
					]
				}
			}`))
		case strings.Contains(r.URL.Path, "/library/metadata/101/children"):
			w.Write([]byte(`{
				"MediaContainer": {
					"Metadata": [
						{
							"ratingKey":"201","title":"Pharaoh's Dance","grandparentTitle":"Miles Davis",
							"parentTitle":"Bitches Brew","year":1970,"index":1,"duration":1140000,
							"Media":[{"Part":[{"key":"/library/parts/2/222/PharaohsDance.flac"}]}]
						}
					]
				}
			}`))
		case strings.HasPrefix(r.URL.Path, "/hubs/sections/"):
			w.Write([]byte(`{"MediaContainer":{"Hub":[]}}`))
		default:
			t.Errorf("unexpected path: %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}
}

func TestProvider_Name(t *testing.T) {
	p := newProvider(NewClient("http://localhost:32400", "tok"))
	if p.Name() != "Plex" {
		t.Errorf("Name() = %q, want %q", p.Name(), "Plex")
	}
}

func TestProvider_Playlists(t *testing.T) {
	srv := httptest.NewServer(sectionsHandler(t))
	defer srv.Close()

	p := newProvider(NewClient(srv.URL, "tok"))
	lists, err := p.Playlists()
	if err != nil {
		t.Fatalf("Playlists() error: %v", err)
	}
	if len(lists) != 3 {
		t.Fatalf("expected 3 entries (1 playlist + 2 albums), got %d", len(lists))
	}

	// First entry is the audio playlist, tagged with its section.
	if lists[0].ID != "pl:24872" {
		t.Errorf("lists[0].ID = %q, want %q", lists[0].ID, "pl:24872")
	}
	if lists[0].Name != "GOOD" {
		t.Errorf("lists[0].Name = %q, want %q", lists[0].Name, "GOOD")
	}
	if lists[0].TrackCount != 3 {
		t.Errorf("lists[0].TrackCount = %d, want 3", lists[0].TrackCount)
	}
	if lists[0].DurationSecs != 600 {
		t.Errorf("lists[0].DurationSecs = %d, want 600", lists[0].DurationSecs)
	}
	if lists[0].Section != "Playlists" {
		t.Errorf("lists[0].Section = %q, want %q", lists[0].Section, "Playlists")
	}

	// Then the albums from the music library.
	if lists[1].ID != "100" {
		t.Errorf("lists[1].ID = %q, want %q", lists[1].ID, "100")
	}
	if !strings.Contains(lists[1].Name, "Miles Davis") {
		t.Errorf("lists[1].Name %q missing artist", lists[1].Name)
	}
	if !strings.Contains(lists[1].Name, "Kind of Blue") {
		t.Errorf("lists[1].Name %q missing album title", lists[1].Name)
	}
	if !strings.Contains(lists[1].Name, "1959") {
		t.Errorf("lists[1].Name %q missing year", lists[1].Name)
	}
	if lists[1].TrackCount != 5 {
		t.Errorf("lists[1].TrackCount = %d, want 5", lists[1].TrackCount)
	}
	if lists[1].Section != "Albums" {
		t.Errorf("lists[1].Section = %q, want %q", lists[1].Section, "Albums")
	}
}

func TestProvider_Playlists_Cached(t *testing.T) {
	callCount := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/library/sections") {
			callCount++
		}
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasSuffix(r.URL.Path, "/playlists"):
			w.Write([]byte(`{"MediaContainer":{"Metadata":[]}}`))
		case strings.HasSuffix(r.URL.Path, "/library/sections"):
			w.Write([]byte(`{"MediaContainer":{"Directory":[{"key":"3","type":"artist","title":"Music"}]}}`))
		case strings.HasSuffix(r.URL.Path, "/library/sections/3/all"):
			w.Write([]byte(`{"MediaContainer":{"totalSize":1,"Metadata":[{"ratingKey":"1","title":"Album","parentTitle":"Artist","year":2020,"leafCount":1}]}}`))
		}
	}))
	defer srv.Close()

	p := newProvider(NewClient(srv.URL, "tok"))
	if _, err := p.Playlists(); err != nil {
		t.Fatalf("first Playlists() error: %v", err)
	}
	if _, err := p.Playlists(); err != nil {
		t.Fatalf("second Playlists() error: %v", err)
	}
	if callCount != 1 {
		t.Errorf("expected /library/sections called once, called %d times", callCount)
	}
}

func TestProvider_Playlists_NoMusicSections(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"MediaContainer":{"Directory":[{"key":"1","type":"movie","title":"Movies"}]}}`))
	}))
	defer srv.Close()

	p := newProvider(NewClient(srv.URL, "tok"))
	_, err := p.Playlists()
	if err == nil {
		t.Fatal("expected error for no music sections, got nil")
	}
	if !strings.Contains(err.Error(), "no matching music libraries") {
		t.Errorf("unexpected error message: %q", err.Error())
	}
}

func TestProvider_Tracks(t *testing.T) {
	srv := httptest.NewServer(sectionsHandler(t))
	defer srv.Close()

	p := newProvider(NewClient(srv.URL, "tok"))
	tracks, err := p.Tracks("100")
	if err != nil {
		t.Fatalf("Tracks() error: %v", err)
	}
	if len(tracks) != 1 {
		t.Fatalf("expected 1 track, got %d", len(tracks))
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
	if tr.DurationSecs != 565 {
		t.Errorf("DurationSecs = %d, want 565", tr.DurationSecs)
	}
	if !tr.Stream {
		t.Error("Stream = false, want true")
	}
	if tr.Path != TrackURIPrefix+"200" {
		t.Errorf("Path = %q, want token-free Plex URI", tr.Path)
	}
	if tr.Meta(provider.MetaPlexID) != "200" {
		t.Errorf("Plex metadata ID = %q, want 200", tr.Meta(provider.MetaPlexID))
	}
}

func TestProvider_Tracks_Playlist(t *testing.T) {
	srv := httptest.NewServer(sectionsHandler(t))
	defer srv.Close()

	p := newProvider(NewClient(srv.URL, "tok"))
	tracks, err := p.Tracks("pl:24872")
	if err != nil {
		t.Fatalf("Tracks(pl:...) error: %v", err)
	}
	if len(tracks) != 1 {
		t.Fatalf("expected 1 track, got %d", len(tracks))
	}

	tr := tracks[0]
	if tr.Title != "Highway to Hell" {
		t.Errorf("Title = %q, want %q", tr.Title, "Highway to Hell")
	}
	if tr.Artist != "AC/DC" {
		t.Errorf("Artist = %q, want %q", tr.Artist, "AC/DC")
	}
	if tr.Album != "Highway to Hell" {
		t.Errorf("Album = %q, want %q", tr.Album, "Highway to Hell")
	}
	// Playlist items carry the album year as parentYear, with no year.
	if tr.Year != 1979 {
		t.Errorf("Year = %d, want the album's 1979", tr.Year)
	}
	if tr.DurationSecs != 208 {
		t.Errorf("DurationSecs = %d, want 208", tr.DurationSecs)
	}
	if !tr.Stream {
		t.Error("Stream = false, want true")
	}
	if tr.Path != TrackURIPrefix+"300" {
		t.Errorf("Path = %q, want token-free Plex URI", tr.Path)
	}
	if favorite, known := p.FavoriteState(tr); !favorite || !known {
		t.Error("Plex userRating=10 was not exposed as a favorite")
	}
}

func TestProviderResolveSourceRestoresAfterRestart(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/library/metadata/200" {
			t.Fatalf("path = %q, want metadata lookup", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"MediaContainer":{"Metadata":[{"ratingKey":"200","userRating":10,"Media":[{"Part":[{"key":"/library/parts/1/111/file.flac"}]}]}]}}`))
	}))
	defer srv.Close()

	p := newProvider(NewClient(srv.URL, "tok"))
	got, err := p.ResolveSource(TrackURIPrefix + "200")
	if err != nil {
		t.Fatalf("ResolveSource: %v", err)
	}
	want := srv.URL + "/library/parts/1/111/file.flac?download=1&X-Plex-Token=tok"
	if got != want {
		t.Fatalf("ResolveSource = %q, want %q", got, want)
	}
	track := playlist.Track{Path: TrackURIPrefix + "200"}
	if favorite, known := p.FavoriteState(track); !favorite || !known {
		t.Fatalf("restored favorite = (%v, %v), want true, true", favorite, known)
	}
}

func TestProviderFavorites(t *testing.T) {
	var gotRating string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/:/rate" {
			t.Fatalf("path = %q, want /:/rate", r.URL.Path)
		}
		gotRating = r.URL.Query().Get("rating")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	p := newProvider(NewClient(srv.URL, "tok"))
	track := playlist.Track{Path: TrackURIPrefix + "200"}
	if !p.CanFavoriteTrack(track) {
		t.Fatal("CanFavoriteTrack = false")
	}
	if err := p.SetTrackFavorite(context.Background(), track, true); err != nil {
		t.Fatalf("SetTrackFavorite: %v", err)
	}
	if gotRating != "10" {
		t.Fatalf("rating = %q, want 10", gotRating)
	}
	if favorite, known := p.FavoriteState(track); !favorite || !known {
		t.Fatal("successful rating did not update the in-memory favorite state")
	}
	if err := p.SetTrackFavorite(context.Background(), track, false); err != nil {
		t.Fatalf("SetTrackFavorite(false): %v", err)
	}
	favorite, known := p.FavoriteState(track)
	if gotRating != "0" || favorite || !known {
		t.Fatalf("cleared rating = %q, state = (%v, %v); want 0, false, true", gotRating, favorite, known)
	}
}

func TestProviderPlaybackReporting(t *testing.T) {
	var timelineStates []string
	scrobbleCount := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/:/timeline":
			timelineStates = append(timelineStates, r.URL.Query().Get("state"))
			if got := r.URL.Query().Get("duration"); got != "120000" {
				t.Errorf("timeline duration = %q, want 120000", got)
			}
		case "/:/scrobble":
			scrobbleCount++
		default:
			t.Fatalf("unexpected path %q", r.URL.Path)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	p := newProvider(NewClient(srv.URL, "tok"))
	track := playlist.Track{Path: TrackURIPrefix + "200", DurationSecs: 120}
	if err := p.ReportNowPlaying(track, 2*time.Second, true); err != nil {
		t.Fatalf("ReportNowPlaying: %v", err)
	}
	if err := p.ReportProgress(track, 30*time.Second); err != nil {
		t.Fatalf("ReportProgress: %v", err)
	}
	if err := p.ReportScrobble(track, 70*time.Second, 2*time.Minute, true); err != nil {
		t.Fatalf("ReportScrobble: %v", err)
	}
	if got := strings.Join(timelineStates, ","); got != "playing,playing,stopped" {
		t.Fatalf("timeline states = %q", got)
	}
	if scrobbleCount != 1 {
		t.Fatalf("scrobble count = %d, want 1", scrobbleCount)
	}
}

func TestProvider_Tracks_Playlist_NotCached(t *testing.T) {
	callCount := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/playlists/24872/items"):
			callCount++
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"MediaContainer":{"totalSize":1,"Metadata":[{"ratingKey":"300","title":"Highway to Hell","grandparentTitle":"AC/DC","parentTitle":"Highway to Hell","index":1,"duration":208000,"Media":[{"Part":[{"key":"/library/parts/3/333/HighwayToHell.flac"}]}]}]}}`))
		default:
			t.Errorf("unexpected path %q", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	p := newProvider(NewClient(srv.URL, "tok"))
	if _, err := p.Tracks("pl:24872"); err != nil {
		t.Fatalf("first Tracks() error: %v", err)
	}
	if _, err := p.Tracks("pl:24872"); err != nil {
		t.Fatalf("second Tracks() error: %v", err)
	}
	if callCount != 2 {
		t.Errorf("expected playlist items endpoint called twice (smart playlists not cached), got %d", callCount)
	}
}

func TestProvider_Tracks_SkipsMissingPart(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{
			"MediaContainer": {
				"Metadata": [
					{"ratingKey":"1","title":"Has Part","Media":[{"Part":[{"key":"/library/parts/1/1/file.mp3"}]}]},
					{"ratingKey":"2","title":"No Part","Media":[]},
					{"ratingKey":"3","title":"Also No Part"}
				]
			}
		}`))
	}))
	defer srv.Close()

	p := newProvider(NewClient(srv.URL, "tok"))
	tracks, err := p.Tracks("42")
	if err != nil {
		t.Fatalf("Tracks() error: %v", err)
	}
	if len(tracks) != 1 {
		t.Errorf("expected 1 track (skipping those with no part), got %d", len(tracks))
	}
	if tracks[0].Title != "Has Part" {
		t.Errorf("expected 'Has Part', got %q", tracks[0].Title)
	}
}

func TestProvider_Tracks_Cached(t *testing.T) {
	callCount := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"MediaContainer":{"Metadata":[{"ratingKey":"1","title":"T","Media":[{"Part":[{"key":"/p/1/1/f.mp3"}]}]}]}}`))
	}))
	defer srv.Close()

	p := newProvider(NewClient(srv.URL, "tok"))
	if _, err := p.Tracks("99"); err != nil {
		t.Fatalf("first Tracks() error: %v", err)
	}
	if _, err := p.Tracks("99"); err != nil {
		t.Fatalf("second Tracks() error: %v", err)
	}
	if callCount != 1 {
		t.Errorf("expected children endpoint called once, called %d times", callCount)
	}
}

func TestProvider_Tracks_AlbumCacheTTL(t *testing.T) {
	callCount := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"MediaContainer":{"Metadata":[{"ratingKey":"1","title":"T%d","Media":[{"Part":[{"key":"/p/1/f.mp3"}]}]}]}}`, callCount)
	}))
	defer srv.Close()

	now := time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)
	p := newProvider(NewClient(srv.URL, "tok"))
	p.now = func() time.Time { return now }
	if _, err := p.Tracks("99"); err != nil {
		t.Fatalf("first Tracks: %v", err)
	}

	now = now.Add(albumCacheTTL - time.Second)
	if _, err := p.Tracks("99"); err != nil {
		t.Fatalf("cached Tracks: %v", err)
	}
	if callCount != 1 {
		t.Fatalf("request count before TTL = %d, want 1", callCount)
	}

	now = now.Add(time.Second)
	refreshed, err := p.Tracks("99")
	if err != nil {
		t.Fatalf("expired Tracks: %v", err)
	}
	if callCount != 2 {
		t.Fatalf("request count after TTL = %d, want 2", callCount)
	}
	if got := refreshed[0].Title; got != "T2" {
		t.Fatalf("title after TTL = %q, want T2", got)
	}
}

func TestProviderRefreshReloadsOpenList(t *testing.T) {
	callCount := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"MediaContainer":{"Metadata":[{"ratingKey":"1","title":"T%d","Media":[{"Part":[{"key":"/p/1/f.mp3"}]}]}]}}`, callCount)
	}))
	defer srv.Close()

	p := newProvider(NewClient(srv.URL, "tok"))
	if _, err := p.Tracks("99"); err != nil {
		t.Fatalf("first Tracks: %v", err)
	}
	p.Refresh()
	refreshed, err := p.Tracks("99")
	if err != nil {
		t.Fatalf("Tracks after Refresh: %v", err)
	}
	if callCount != 2 {
		t.Fatalf("request count after Refresh = %d, want 2", callCount)
	}
	if got := refreshed[0].Title; got != "T2" {
		t.Fatalf("title after Refresh = %q, want T2", got)
	}
}

func TestProviderCanRefreshPlaylist(t *testing.T) {
	p := newProvider(NewClient("http://localhost:32400", "tok"))
	for _, tt := range []struct {
		id   string
		want bool
	}{
		{id: "99", want: true},
		{id: "album_key-1", want: true},
		{id: "pl:24872", want: true},
		{id: "", want: false},
		{id: "pl:", want: false},
		{id: "browse/albums", want: false},
	} {
		if got := p.CanRefreshPlaylist(tt.id); got != tt.want {
			t.Errorf("CanRefreshPlaylist(%q) = %v, want %v", tt.id, got, tt.want)
		}
	}
}

func TestProvider_Tracks_ClientError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	p := newProvider(NewClient(srv.URL, "bad-token"))
	_, err := p.Tracks("42")
	if err == nil {
		t.Fatal("expected error on 401, got nil")
	}
}

func TestNewFromConfig_NilWhenMissing(t *testing.T) {
	tests := []struct {
		name string
		cfg  config.PlexConfig
	}{
		{"empty", config.PlexConfig{}},
		{"no token", config.PlexConfig{URL: "http://localhost:32400"}},
		{"no url", config.PlexConfig{Token: "tok"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if p := NewFromConfig(tt.cfg); p != nil {
				t.Errorf("NewFromConfig(%+v) = non-nil, want nil", tt.cfg)
			}
		})
	}
}

func TestNewFromConfig_OK(t *testing.T) {
	cfg := config.PlexConfig{URL: "http://localhost:32400", Token: "mytoken"}
	if p := NewFromConfig(cfg); p == nil {
		t.Error("NewFromConfig() returned nil for valid config")
	}
}

func TestNewFromConfig_Account(t *testing.T) {
	cfg := config.PlexConfig{
		URL:      "https://old.plex.direct:32400",
		Token:    "account-token",
		ClientID: "client-1",
		ServerID: "server-1",
	}
	if p := NewFromConfig(cfg); p == nil || p.client.account == nil {
		t.Fatal("NewFromConfig() did not create an account-backed client")
	}
}

func TestNewFromConfig_LibrariesWired(t *testing.T) {
	tests := []struct {
		name      string
		libraries []string
		wantLen   int
	}{
		{"no filter returns all", nil, 2},
		{"filter to Jazz", []string{"Jazz"}, 1},
		{"filter to jazz case-insensitive", []string{"jazz"}, 1},
		{"filter to both", []string{"Music", "Jazz"}, 2},
		{"no match returns error", []string{"Classical"}, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				switch {
				case strings.HasSuffix(r.URL.Path, "/playlists"):
					w.Write([]byte(`{"MediaContainer":{"Metadata":[]}}`))
				case strings.HasSuffix(r.URL.Path, "/library/sections"):
					w.Write([]byte(`{"MediaContainer":{"Directory":[
						{"key":"1","type":"artist","title":"Music"},
						{"key":"2","type":"artist","title":"Jazz"}
					]}}`))
				case strings.HasSuffix(r.URL.Path, "/library/sections/1/all"):
					w.Write([]byte(`{"MediaContainer":{"totalSize":1,"Metadata":[
						{"ratingKey":"11","title":"Bitches Brew","parentTitle":"Miles Davis","year":1970,"leafCount":4}
					]}}`))
				case strings.HasSuffix(r.URL.Path, "/library/sections/2/all"):
					w.Write([]byte(`{"MediaContainer":{"totalSize":1,"Metadata":[
						{"ratingKey":"10","title":"Kind of Blue","parentTitle":"Miles Davis","year":1959,"leafCount":5}
					]}}`))
				default:
					w.WriteHeader(http.StatusNotFound)
				}
			}))
			defer srv.Close()

			cfg := config.PlexConfig{
				URL:       srv.URL,
				Token:     "tok",
				Libraries: tt.libraries,
			}
			p := NewFromConfig(cfg)
			if p == nil {
				t.Fatal("NewFromConfig() returned nil for valid config")
			}

			lists, err := p.Playlists()
			if tt.wantLen == 0 {
				if err == nil {
					t.Errorf("expected error for no matching libraries, got %d playlists", len(lists))
				}
				return
			}
			if err != nil {
				t.Fatalf("Playlists() error: %v", err)
			}
			if len(lists) != tt.wantLen {
				t.Fatalf("got %d playlists, want %d", len(lists), tt.wantLen)
			}
		})
	}
}

func TestProviderRestoreTrack(t *testing.T) {
	p := newProvider(NewClient("http://plex.example.com:32400", "token"))
	tests := []struct {
		name string
		path string
		want bool
	}{
		{name: "plex track", path: TrackURIPrefix + "200", want: true},
		{name: "token URL", path: "http://plex.example.com:32400/library/parts/1/file.flac?X-Plex-Token=token"},
		{name: "local file", path: "/music/song.mp3"},
		{name: "bad rating key", path: TrackURIPrefix + "../200"},
		{name: "empty rating key", path: TrackURIPrefix},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			in := playlist.Track{Path: tt.path, Title: "Song", ProviderMeta: map[string]string{"other": "kept"}}
			got, ok := p.RestoreTrack(in)
			if ok != tt.want {
				t.Fatalf("RestoreTrack ok = %v, want %v", ok, tt.want)
			}
			if !ok {
				return
			}
			if got.Path != tt.path || got.Title != "Song" || !got.Stream {
				t.Fatalf("restored track = %+v", got)
			}
			if got.Meta(provider.MetaPlexID) != "200" || got.Meta("other") != "kept" {
				t.Fatalf("restored meta = %v", got.ProviderMeta)
			}
			if in.ProviderMeta[provider.MetaPlexID] != "" {
				t.Fatal("RestoreTrack mutated the caller's metadata map")
			}
		})
	}
}

// stationServer serves one music section with four stations, the machine
// identity, and play queues. It counts the play queues started.
func stationServer(t *testing.T, hubStatus int) (*httptest.Server, *int) {
	t.Helper()
	queues := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/playlists":
			w.Write([]byte(`{"MediaContainer":{"Metadata":[{"ratingKey":"5","title":"Mix","playlistType":"audio","leafCount":2}]}}`))
		case r.URL.Path == "/library/sections":
			w.Write([]byte(`{"MediaContainer":{"Directory":[{"key":"3","type":"artist","title":"Music"}]}}`))
		case r.URL.Path == "/library/sections/3/all":
			w.Write([]byte(`{"MediaContainer":{"totalSize":1,"Metadata":[{"ratingKey":"1","title":"Album","parentTitle":"Artist","leafCount":1}]}}`))
		case r.URL.Path == "/hubs/sections/3":
			if hubStatus != http.StatusOK {
				w.WriteHeader(hubStatus)
				return
			}
			w.Write([]byte(`{"MediaContainer":{"Hub":[
				{"type":"album","Metadata":[{"key":"/library/metadata/1","title":"Album"}]},
				{"type":"station","Metadata":[
					{"key":"/library/sections/3/stations/1","title":"Library Radio"},
					{"key":"/library/sections/3/stations/8","title":"Deep Cuts Radio"},
					{"key":"/library/sections/3/stations/2","title":"Time Travel Radio"},
					{"key":"/library/sections/3/stations/3","title":"Random Album Radio"}
				]}
			]}}`))
		case r.URL.Path == "/identity":
			w.Write([]byte(`{"MediaContainer":{"machineIdentifier":"machine-1"}}`))
		case r.Method == http.MethodPost && r.URL.Path == "/playQueues":
			if got, want := r.URL.Query().Get("uri"), "server://machine-1/com.plexapp.plugins.library/library/sections/3/stations/3"; got != want {
				t.Errorf("play queue uri = %q, want %q", got, want)
			}
			if got := r.URL.Query().Get("type"); got != "audio" {
				t.Errorf("play queue type = %q", got)
			}
			queues++
			fmt.Fprintf(w, `{"MediaContainer":{"playQueueID":%d}}`, 3300+queues)
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/playQueues/"):
			q := r.URL.Query()
			// The first window also returns the selected item; a
			// continuation window starts after its center.
			wantWindow, item := strconv.Itoa(stationBatch-1), 100*queues
			if center := q.Get("center"); center != "" {
				if q.Get("includeBefore") != "0" {
					t.Errorf("continuation includeBefore = %q, want 0", q.Get("includeBefore"))
				}
				wantWindow = strconv.Itoa(stationBatch)
				item, _ = strconv.Atoi(center)
				item++
			}
			if got := q.Get("window"); got != wantWindow {
				t.Errorf("window = %q, want %s", got, wantWindow)
			}
			fmt.Fprintf(w, `{"MediaContainer":{"Metadata":[
				{"ratingKey":"7%d","title":"Song","grandparentTitle":"Band","duration":1000,"playQueueItemID":%d,"Media":[{"Part":[{"key":"/library/parts/7/file.flac"}]}]}
			]}}`, item, item)
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	return srv, &queues
}

func TestProviderStationLists(t *testing.T) {
	tests := []struct {
		name      string
		stations  []string
		hubStatus int
		want      []string
	}{
		{name: "all by default", hubStatus: http.StatusOK,
			want: []string{"Library Radio", "Deep Cuts Radio", "Time Travel Radio", "Random Album Radio"}},
		{name: "picked in setting order", stations: []string{"random album radio", "Library Radio", "Missing"}, hubStatus: http.StatusOK,
			want: []string{"Random Album Radio", "Library Radio"}},
		{name: "hidden", stations: []string{}, hubStatus: http.StatusOK},
		{name: "hub error keeps the library", hubStatus: http.StatusInternalServerError},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv, _ := stationServer(t, tt.hubStatus)
			p := newProvider(NewClient(srv.URL, "tok"))
			p.stations = tt.stations
			lists, err := p.Playlists()
			if err != nil {
				t.Fatalf("Playlists: %v", err)
			}
			var got []string
			for _, l := range lists {
				if l.Section == sectionRadio {
					if !strings.HasPrefix(l.ID, prefixStation) {
						t.Errorf("station ID %q lacks the prefix", l.ID)
					}
					got = append(got, l.Name)
				}
			}
			if !slices.Equal(got, tt.want) {
				t.Fatalf("stations = %v, want %v", got, tt.want)
			}
			// Playlists, then radio stations, then albums.
			var sections []string
			for _, l := range lists {
				if len(sections) == 0 || sections[len(sections)-1] != l.Section {
					sections = append(sections, l.Section)
				}
			}
			want := []string{sectionPlaylists, sectionRadio, sectionAlbums}
			if len(got) == 0 {
				want = []string{sectionPlaylists, sectionAlbums}
			}
			if !slices.Equal(sections, want) {
				t.Fatalf("section order = %v, want %v", sections, want)
			}
		})
	}
}

func TestProviderStationTracksStartFreshQueues(t *testing.T) {
	srv, queues := stationServer(t, http.StatusOK)
	p := newProvider(NewClient(srv.URL, "tok"))
	id := prefixStation + "/library/sections/3/stations/3"

	first, err := p.Tracks(id)
	if err != nil {
		t.Fatalf("Tracks: %v", err)
	}
	second, err := p.Tracks(id)
	if err != nil {
		t.Fatalf("second Tracks: %v", err)
	}
	if *queues != 2 {
		t.Fatalf("play queues started = %d, want 2 (stations are never cached)", *queues)
	}
	if len(first) != 1 || first[0].Path != TrackURIPrefix+"7100" || second[0].Path != TrackURIPrefix+"7200" {
		t.Fatalf("station tracks = %+v then %+v", first, second)
	}
}

// Stations come only from the libraries the libraries setting selects, and
// carry the library name only when more than one library is shown.
func TestProviderStationsFollowLibraries(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/playlists":
			w.Write([]byte(`{"MediaContainer":{"Metadata":[]}}`))
		case r.URL.Path == "/library/sections":
			w.Write([]byte(`{"MediaContainer":{"Directory":[
				{"key":"3","type":"artist","title":"Music"},
				{"key":"4","type":"artist","title":"Jazz"}
			]}}`))
		case strings.HasSuffix(r.URL.Path, "/all"):
			w.Write([]byte(`{"MediaContainer":{"totalSize":1,"Metadata":[{"ratingKey":"1","title":"Album","parentTitle":"Artist","leafCount":1}]}}`))
		case r.URL.Path == "/hubs/sections/3" || r.URL.Path == "/hubs/sections/4":
			key := strings.TrimPrefix(r.URL.Path, "/hubs/sections/")
			fmt.Fprintf(w, `{"MediaContainer":{"Hub":[{"type":"station","Metadata":[
				{"key":"/library/sections/%s/stations/1","title":"Library Radio"}
			]}]}}`, key)
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	tests := []struct {
		name      string
		libraries []string
		want      []string
	}{
		{name: "one selected library", libraries: []string{"jazz"}, want: []string{"Library Radio"}},
		{name: "all libraries", want: []string{"Library Radio · Music", "Library Radio · Jazz"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := newProvider(NewClient(srv.URL, "tok", tt.libraries...))
			lists, err := p.Playlists()
			if err != nil {
				t.Fatalf("Playlists: %v", err)
			}
			var got []string
			for _, l := range lists {
				if l.Section == sectionRadio {
					got = append(got, l.Name)
				}
			}
			if !slices.Equal(got, tt.want) {
				t.Fatalf("stations = %v, want %v", got, tt.want)
			}
			for _, l := range lists {
				if tt.libraries != nil && l.Section == sectionRadio && !strings.Contains(l.ID, "/sections/4/") {
					t.Fatalf("station %q is not from the selected library", l.ID)
				}
			}
		})
	}
}

// The last track of a station batch continues the same server play queue,
// once; a failed continuation can be retried.
func TestProviderStationContinuesAfterLastTrack(t *testing.T) {
	srv, queues := stationServer(t, http.StatusOK)
	p := newProvider(NewClient(srv.URL, "tok"))
	batch, err := p.Tracks(prefixStation + "/library/sections/3/stations/3")
	if err != nil {
		t.Fatalf("Tracks: %v", err)
	}
	last := batch[len(batch)-1]
	if !p.CanContinueAfter(last) || p.CanContinueAfter(playlist.Track{Path: TrackURIPrefix + "1"}) {
		t.Fatal("CanContinueAfter does not single out the batch's last track")
	}

	next, err := p.ContinueAfter(last)
	if err != nil {
		t.Fatalf("ContinueAfter: %v", err)
	}
	if len(next) != 1 || next[0].Path != TrackURIPrefix+"7101" {
		t.Fatalf("next batch = %+v, want the item after 100", next)
	}
	if *queues != 1 {
		t.Fatalf("play queues started = %d, want 1: continuing reuses the queue", *queues)
	}
	if p.CanContinueAfter(last) {
		t.Fatal("the old batch end still continues")
	}
	if !p.CanContinueAfter(next[0]) {
		t.Fatal("the new batch end does not continue")
	}

	srv.Close()
	if _, err := p.ContinueAfter(next[0]); err == nil {
		t.Fatal("ContinueAfter succeeded against a closed server")
	}
	if !p.CanContinueAfter(next[0]) {
		t.Fatal("a failed continuation dropped the queue")
	}
}

func TestProviderLoadFavoriteStates(t *testing.T) {
	var (
		mu       sync.Mutex
		requests []string
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		keys, ok := strings.CutPrefix(r.URL.Path, "/library/metadata/")
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		mu.Lock()
		requests = append(requests, keys)
		mu.Unlock()
		var items []string
		for _, k := range strings.Split(keys, ",") {
			rating := 0
			if n, _ := strconv.Atoi(k); n%2 == 0 {
				rating = 10
			}
			items = append(items, fmt.Sprintf(`{"ratingKey":%q,"title":"T%s","userRating":%d}`, k, k, rating))
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"MediaContainer":{"Metadata":[%s]}}`, strings.Join(items, ","))
	}))
	defer srv.Close()
	sent := func() []string {
		mu.Lock()
		defer mu.Unlock()
		return slices.Clone(requests)
	}

	p := newProvider(NewClient(srv.URL, "tok"))
	// A state known before the load, as from a toggle, is kept and not asked.
	p.favorites = map[string]bool{"1": true}

	var tracks []playlist.Track
	for i := 1; i <= favoriteBatch+2; i++ {
		tracks = append(tracks, playlist.Track{Path: TrackURIPrefix + strconv.Itoa(i)})
	}
	tracks = append(tracks, tracks[1], playlist.Track{Path: "/local/song.mp3"})
	if err := p.LoadFavoriteStates(t.Context(), tracks); err != nil {
		t.Fatalf("LoadFavoriteStates: %v", err)
	}

	got := sent()
	if len(got) != 2 {
		t.Fatalf("requests = %d, want 2 batches", len(got))
	}
	if first := strings.Split(got[0], ","); len(first) != favoriteBatch || first[0] != "2" {
		t.Fatalf("first batch = %d keys from %q, want %d from \"2\"", len(first), first[0], favoriteBatch)
	}
	for _, tt := range []struct {
		id   string
		want bool
	}{{"1", true}, {"2", true}, {"3", false}, {strconv.Itoa(favoriteBatch + 2), true}} {
		got, known := p.FavoriteState(playlist.Track{Path: TrackURIPrefix + tt.id})
		if !known || got != tt.want {
			t.Errorf("FavoriteState(%s) = %v, %v; want %v, true", tt.id, got, known, tt.want)
		}
	}

	// Every state is known now, so a second load sends nothing.
	if err := p.LoadFavoriteStates(t.Context(), tracks); err != nil || len(sent()) != 2 {
		t.Fatalf("second load: err %v, requests %d; want none", err, len(sent()))
	}
}
