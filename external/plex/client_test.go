package plex

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// newTestClient returns a Client pointed at the given test server.
func newTestClient(srv *httptest.Server) *Client {
	return NewClient(srv.URL, "test-token")
}

func TestPing_OK(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("X-Plex-Token") != "test-token" {
			t.Errorf("expected token in query, got %q", r.URL.Query().Get("X-Plex-Token"))
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"MediaContainer":{"friendlyName":"My Plex"}}`))
	}))
	defer srv.Close()

	if err := newTestClient(srv).Ping(); err != nil {
		t.Fatalf("Ping() unexpected error: %v", err)
	}
}

func TestPing_Unauthorized(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	err := newTestClient(srv).Ping()
	if err == nil {
		t.Fatal("Ping() expected error on 401, got nil")
	}
	if !strings.Contains(err.Error(), "token invalid") {
		t.Errorf("expected 'token invalid' in error, got %q", err.Error())
	}
}

func TestPing_ServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	err := newTestClient(srv).Ping()
	if err == nil {
		t.Fatal("Ping() expected error on 500, got nil")
	}
}

func TestMusicSections_FiltersByType(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{
			"MediaContainer": {
				"Directory": [
					{"key": "1", "type": "artist", "title": "Music"},
					{"key": "2", "type": "movie",  "title": "Movies"},
					{"key": "3", "type": "artist", "title": "Jazz"}
				]
			}
		}`))
	}))
	defer srv.Close()

	sections, err := newTestClient(srv).MusicSections()
	if err != nil {
		t.Fatalf("MusicSections() error: %v", err)
	}
	if len(sections) != 2 {
		t.Fatalf("expected 2 music sections, got %d", len(sections))
	}
	if sections[0].Key != "1" || sections[0].Title != "Music" {
		t.Errorf("unexpected section[0]: %+v", sections[0])
	}
	if sections[1].Key != "3" || sections[1].Title != "Jazz" {
		t.Errorf("unexpected section[1]: %+v", sections[1])
	}
}

func TestMusicSections_Empty(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"MediaContainer":{"Directory":[{"key":"1","type":"movie","title":"Movies"}]}}`))
	}))
	defer srv.Close()

	sections, err := newTestClient(srv).MusicSections()
	if err != nil {
		t.Fatalf("MusicSections() unexpected error: %v", err)
	}
	if len(sections) != 0 {
		t.Errorf("expected 0 music sections, got %d", len(sections))
	}
}

func TestAlbums_RequestsType9(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("type") != "9" {
			t.Errorf("expected type=9 in request, got %q", r.URL.Query().Get("type"))
		}
		if !strings.HasSuffix(r.URL.Path, "/library/sections/3/all") {
			t.Errorf("unexpected path %q", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{
			"MediaContainer": {
				"totalSize": 2,
				"Metadata": [
					{
						"ratingKey": "456",
						"title": "Kind of Blue",
						"parentTitle": "Miles Davis",
						"year": 1959,
						"leafCount": 5
					},
					{
						"ratingKey": "457",
						"title": "Bitches Brew",
						"parentTitle": "Miles Davis",
						"year": 1970,
						"leafCount": 4
					}
				]
			}
		}`))
	}))
	defer srv.Close()

	albums, err := newTestClient(srv).Albums("3")
	if err != nil {
		t.Fatalf("Albums() error: %v", err)
	}
	if len(albums) != 2 {
		t.Fatalf("expected 2 albums, got %d", len(albums))
	}
	a := albums[0]
	if a.RatingKey != "456" || a.Title != "Kind of Blue" || a.ArtistName != "Miles Davis" || a.Year != 1959 || a.TrackCount != 5 {
		t.Errorf("unexpected album[0]: %+v", a)
	}
}

func TestAlbums_Paginates(t *testing.T) {
	callCount := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		start := r.URL.Query().Get("X-Plex-Container-Start")
		w.Header().Set("Content-Type", "application/json")
		switch start {
		case "0", "":
			w.Write([]byte(`{"MediaContainer":{"totalSize":2,"Metadata":[{"ratingKey":"1","title":"A","parentTitle":"Art","year":2000,"leafCount":1}]}}`))
		case "300":
			// Second page — but totalSize=2 means we should never reach here with pageSize=300.
			// This tests that we stop after the first page when totalSize <= pageSize.
			t.Errorf("unexpected second page request (offset=%s)", start)
			w.Write([]byte(`{"MediaContainer":{"totalSize":2,"Metadata":[]}}`))
		}
	}))
	defer srv.Close()

	albums, err := newTestClient(srv).Albums("1")
	if err != nil {
		t.Fatalf("Albums() error: %v", err)
	}
	if len(albums) != 1 {
		t.Errorf("expected 1 album, got %d", len(albums))
	}
	if callCount != 1 {
		t.Errorf("expected 1 API call, got %d", callCount)
	}
}

func TestTracks_MapsAllFields(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/library/metadata/456/children") {
			t.Errorf("unexpected path %q", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{
			"MediaContainer": {
				"Metadata": [
					{
						"ratingKey": "789",
						"title": "So What",
						"grandparentTitle": "Miles Davis",
						"parentTitle": "Kind of Blue",
						"year": 1959,
						"index": 1,
						"duration": 565000,
						"Media": [{"Part": [{"key": "/library/parts/100/111/So_What.flac"}]}]
					},
					{
						"ratingKey": "790",
						"title": "Freddie Freeloader",
						"grandparentTitle": "Miles Davis",
						"parentTitle": "Kind of Blue",
						"year": 1959,
						"index": 2,
						"duration": 586000,
						"Media": [{"Part": [{"key": "/library/parts/101/222/Freddie.flac"}]}]
					}
				]
			}
		}`))
	}))
	defer srv.Close()

	tracks, err := newTestClient(srv).Tracks("456")
	if err != nil {
		t.Fatalf("Tracks() error: %v", err)
	}
	if len(tracks) != 2 {
		t.Fatalf("expected 2 tracks, got %d", len(tracks))
	}
	tr := tracks[0]
	if tr.RatingKey != "789" {
		t.Errorf("RatingKey: got %q, want %q", tr.RatingKey, "789")
	}
	if tr.Title != "So What" {
		t.Errorf("Title: got %q, want %q", tr.Title, "So What")
	}
	if tr.ArtistName != "Miles Davis" {
		t.Errorf("ArtistName: got %q, want %q", tr.ArtistName, "Miles Davis")
	}
	if tr.AlbumName != "Kind of Blue" {
		t.Errorf("AlbumName: got %q, want %q", tr.AlbumName, "Kind of Blue")
	}
	if tr.Year != 1959 {
		t.Errorf("Year: got %d, want 1959", tr.Year)
	}
	if tr.TrackNumber != 1 {
		t.Errorf("TrackNumber: got %d, want 1", tr.TrackNumber)
	}
	if tr.Duration != 565000 {
		t.Errorf("Duration: got %d, want 565000", tr.Duration)
	}
	if tr.PartKey != "/library/parts/100/111/So_What.flac" {
		t.Errorf("PartKey: got %q, want /library/parts/100/111/So_What.flac", tr.PartKey)
	}
}

func TestTracks_MissingMedia(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{
			"MediaContainer": {
				"Metadata": [
					{"ratingKey": "1", "title": "Track Without Media", "Media": []}
				]
			}
		}`))
	}))
	defer srv.Close()

	tracks, err := newTestClient(srv).Tracks("42")
	if err != nil {
		t.Fatalf("Tracks() error: %v", err)
	}
	if len(tracks) != 1 {
		t.Fatalf("expected 1 track, got %d", len(tracks))
	}
	if tracks[0].PartKey != "" {
		t.Errorf("expected empty PartKey for track with no media, got %q", tracks[0].PartKey)
	}
}

func TestStreamURL_Format(t *testing.T) {
	c := NewClient("http://192.168.1.10:32400", "mytoken")
	got := c.StreamURL("/library/parts/100/111/file.flac")
	want := "http://192.168.1.10:32400/library/parts/100/111/file.flac?X-Plex-Token=mytoken"
	if got != want {
		t.Errorf("StreamURL:\n got  %q\n want %q", got, want)
	}
}

func TestStreamURL_TokenEncoded(t *testing.T) {
	c := NewClient("http://192.168.1.10:32400", "tok en+special")
	got := c.StreamURL("/library/parts/1/2/file.mp3")
	if !strings.Contains(got, "X-Plex-Token=tok+en%2Bspecial") {
		t.Errorf("StreamURL token not URL-encoded: %q", got)
	}
}

func TestSearch_SendsCorrectParams(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("query") != "Miles Davis" {
			t.Errorf("expected query=Miles Davis, got %q", r.URL.Query().Get("query"))
		}
		if r.URL.Query().Get("type") != "10" {
			t.Errorf("expected type=10, got %q", r.URL.Query().Get("type"))
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"MediaContainer":{"Metadata":[]}}`))
	}))
	defer srv.Close()

	tracks, err := newTestClient(srv).Search("Miles Davis")
	if err != nil {
		t.Fatalf("Search() error: %v", err)
	}
	if len(tracks) != 0 {
		t.Errorf("expected 0 tracks, got %d", len(tracks))
	}
}

func TestSearch_EmptyQueryNoRequest(t *testing.T) {
	called := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	tracks, err := newTestClient(srv).Search("")
	if err != nil {
		t.Fatalf("Search(\"\") unexpected error: %v", err)
	}
	if tracks != nil {
		t.Errorf("expected nil tracks for empty query, got %v", tracks)
	}
	if called {
		t.Error("Search(\"\") should not make an HTTP request")
	}
}

func TestRequestHeaders(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Accept") != "application/json" {
			t.Errorf("expected Accept: application/json, got %q", r.Header.Get("Accept"))
		}
		if r.Header.Get("X-Plex-Product") != "cliamp" {
			t.Errorf("expected X-Plex-Product: cliamp, got %q", r.Header.Get("X-Plex-Product"))
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"MediaContainer":{}}`))
	}))
	defer srv.Close()

	_ = newTestClient(srv).Ping()
}
