package plex

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"cliamp/config"
)

// sectionsHandler returns a handler that serves a single music section.
func sectionsHandler(t *testing.T) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
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
	if len(lists) != 2 {
		t.Fatalf("expected 2 playlists, got %d", len(lists))
	}

	// Check first album entry
	if lists[0].ID != "100" {
		t.Errorf("lists[0].ID = %q, want %q", lists[0].ID, "100")
	}
	if !strings.Contains(lists[0].Name, "Miles Davis") {
		t.Errorf("lists[0].Name %q missing artist", lists[0].Name)
	}
	if !strings.Contains(lists[0].Name, "Kind of Blue") {
		t.Errorf("lists[0].Name %q missing album title", lists[0].Name)
	}
	if !strings.Contains(lists[0].Name, "1959") {
		t.Errorf("lists[0].Name %q missing year", lists[0].Name)
	}
	if lists[0].TrackCount != 5 {
		t.Errorf("lists[0].TrackCount = %d, want 5", lists[0].TrackCount)
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
	if !strings.Contains(err.Error(), "no music libraries") {
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
	if !strings.HasPrefix(tr.Path, srv.URL) {
		t.Errorf("Path %q does not start with server URL", tr.Path)
	}
	if !strings.Contains(tr.Path, "X-Plex-Token=tok") {
		t.Errorf("Path %q missing X-Plex-Token", tr.Path)
	}
	if !strings.Contains(tr.Path, "/library/parts/1/111/SoWhat.flac") {
		t.Errorf("Path %q missing part key", tr.Path)
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
