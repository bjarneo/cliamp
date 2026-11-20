package plex

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bjarneo/cliamp/internal/httpclient"
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

func TestMusicSections_LibraryFilter(t *testing.T) {
	serverJSON := `{"MediaContainer":{"Directory":[
		{"key":"1","type":"artist","title":"Music"},
		{"key":"2","type":"artist","title":"Jazz"},
		{"key":"3","type":"artist","title":"Classical"}
	]}}`

	tests := []struct {
		name       string
		libraries  []string
		wantLen    int
		wantTitles []string
		wantKeys   []string
	}{
		{
			name:       "filter to subset",
			libraries:  []string{"Jazz", "Classical"},
			wantLen:    2,
			wantTitles: []string{"Jazz", "Classical"},
		},
		{
			name:      "case-insensitive match",
			libraries: []string{"MUSIC"},
			wantLen:   1,
			wantKeys:  []string{"1"},
		},
		{
			name:      "no match returns empty",
			libraries: []string{"DoesNotExist"},
			wantLen:   0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.Write([]byte(serverJSON))
			}))
			defer srv.Close()

			sections, err := NewClient(srv.URL, "test-token", tt.libraries...).MusicSections()
			if err != nil {
				t.Fatalf("MusicSections() error: %v", err)
			}
			if len(sections) != tt.wantLen {
				t.Fatalf("got %d sections, want %d: %v", len(sections), tt.wantLen, sections)
			}
			for i, title := range tt.wantTitles {
				if sections[i].Title != title {
					t.Errorf("sections[%d].Title = %q, want %q", i, sections[i].Title, title)
				}
			}
			for i, key := range tt.wantKeys {
				if sections[i].Key != key {
					t.Errorf("sections[%d].Key = %q, want %q", i, sections[i].Key, key)
				}
			}
		})
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
	seenStarts := map[string]bool{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := r.URL.Query().Get("X-Plex-Container-Start")
		seenStarts[start] = true
		w.Header().Set("Content-Type", "application/json")
		switch start {
		case "0", "":
			// Two albums for a page of 300: the server returned fewer
			// items than were asked for, which is the case that used to
			// make the loop skip straight past the remainder.
			w.Write([]byte(`{"MediaContainer":{"totalSize":3,"Metadata":[{"ratingKey":"1","title":"A","parentTitle":"Art","year":2000,"leafCount":1},{"ratingKey":"2","title":"B","parentTitle":"Art","year":2001,"leafCount":2}]}}`))
		case "2":
			w.Write([]byte(`{"MediaContainer":{"totalSize":3,"Metadata":[{"ratingKey":"3","title":"C","parentTitle":"Art","year":2002,"leafCount":3}]}}`))
		default:
			t.Errorf("unexpected start %q", start)
			w.WriteHeader(http.StatusBadRequest)
		}
	}))
	defer srv.Close()

	albums, err := newTestClient(srv).Albums("1")
	if err != nil {
		t.Fatalf("Albums() error: %v", err)
	}
	if len(albums) != 3 {
		t.Fatalf("expected 3 albums, got %d", len(albums))
	}
	if albums[2].Title != "C" || albums[2].TrackCount != 3 {
		t.Errorf("unexpected albums[2]: %+v", albums[2])
	}
	if !seenStarts["0"] || !seenStarts["2"] {
		t.Errorf("expected pagination to hit starts 0 and 2, got %v", seenStarts)
	}
}

func TestAlbums_PaginatesWithoutTotalSize(t *testing.T) {
	var starts []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := r.URL.Query().Get("X-Plex-Container-Start")
		starts = append(starts, start)
		w.Header().Set("Content-Type", "application/json")
		switch start {
		case "0", "":
			w.Write([]byte(`{"MediaContainer":{"Metadata":[{"ratingKey":"1","title":"A"}]}}`))
		case "1":
			w.Write([]byte(`{"MediaContainer":{"Metadata":[{"ratingKey":"2","title":"B"}]}}`))
		case "2":
			w.Write([]byte(`{"MediaContainer":{"Metadata":[]}}`))
		default:
			t.Errorf("unexpected start %q", start)
			w.WriteHeader(http.StatusBadRequest)
		}
	}))
	defer srv.Close()

	albums, err := newTestClient(srv).Albums("1")
	if err != nil {
		t.Fatalf("Albums() error: %v", err)
	}
	if len(albums) != 2 {
		t.Fatalf("expected 2 albums, got %d", len(albums))
	}
	wantStarts := []string{"0", "1", "2"}
	if !slices.Equal(starts, wantStarts) {
		t.Fatalf("starts = %v, want %v", starts, wantStarts)
	}
}

func TestAlbums_StopsWhenTotalSizeReached(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"MediaContainer":{"totalSize":1,"Metadata":[{"ratingKey":"1","title":"A"}]}}`))
	}))
	defer srv.Close()

	albums, err := newTestClient(srv).Albums("1")
	if err != nil {
		t.Fatalf("Albums() error: %v", err)
	}
	if len(albums) != 1 {
		t.Fatalf("expected 1 album, got %d", len(albums))
	}
	if calls != 1 {
		t.Fatalf("expected 1 API call once totalSize was reached, got %d", calls)
	}
}

func TestPlaylists_FiltersAudio(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/playlists") {
			t.Errorf("unexpected path %q", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{
			"MediaContainer": {
				"Metadata": [
					{"ratingKey":"1","title":"GOOD","smart":true,"playlistType":"audio","leafCount":3,"duration":600000},
					{"ratingKey":"2","title":"drums","smart":false,"playlistType":"audio","leafCount":28,"duration":6000000},
					{"ratingKey":"3","title":"Home Movies","smart":false,"playlistType":"video","leafCount":4,"duration":12000000}
				]
			}
		}`))
	}))
	defer srv.Close()

	playlists, err := newTestClient(srv).Playlists()
	if err != nil {
		t.Fatalf("Playlists() error: %v", err)
	}
	if len(playlists) != 2 {
		t.Fatalf("expected 2 audio playlists, got %d", len(playlists))
	}
	a := playlists[0]
	if a.RatingKey != "1" || a.Title != "GOOD" || !a.Smart || a.TrackCount != 3 || a.DurationSecs != 600 {
		t.Errorf("unexpected playlist[0]: %+v", a)
	}
	b := playlists[1]
	if b.RatingKey != "2" || b.Title != "drums" || b.Smart || b.TrackCount != 28 || b.DurationSecs != 6000 {
		t.Errorf("unexpected playlist[1]: %+v", b)
	}
}

func TestPlaylistTracks_Paginates(t *testing.T) {
	seenStarts := map[string]bool{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/playlists/42/items") {
			t.Errorf("unexpected path %q", r.URL.Path)
		}
		start := r.URL.Query().Get("X-Plex-Container-Start")
		size := r.URL.Query().Get("X-Plex-Container-Size")
		if size != "1000" {
			t.Errorf("expected size=1000, got %q", size)
		}
		seenStarts[start] = true
		w.Header().Set("Content-Type", "application/json")
		switch start {
		case "0", "":
			w.Write([]byte(`{"MediaContainer":{"totalSize":3,"Metadata":[{"ratingKey":"1","title":"One","grandparentTitle":"A","parentTitle":"Al","index":1,"duration":1000,"Media":[{"Part":[{"key":"/library/parts/1/1/one.flac"}]}]},{"ratingKey":"2","title":"Two","grandparentTitle":"B","parentTitle":"Alb","index":2,"duration":2000,"Media":[{"Part":[{"key":"/library/parts/2/2/two.flac"}]}]}]}}`))
		case "2":
			w.Write([]byte(`{"MediaContainer":{"totalSize":3,"Metadata":[{"ratingKey":"3","title":"Three","grandparentTitle":"C","parentTitle":"Album","index":3,"duration":3000,"Media":[{"Part":[{"key":"/library/parts/3/3/three.flac"}]}]}]}}`))
		default:
			t.Errorf("unexpected start %q", start)
			w.WriteHeader(http.StatusBadRequest)
		}
	}))
	defer srv.Close()

	tracks, err := newTestClient(srv).PlaylistTracks("42")
	if err != nil {
		t.Fatalf("PlaylistTracks() error: %v", err)
	}
	if len(tracks) != 3 {
		t.Fatalf("expected 3 tracks, got %d", len(tracks))
	}
	if tracks[0].Title != "One" || tracks[0].PartKey != "/library/parts/1/1/one.flac" {
		t.Errorf("unexpected tracks[0]: %+v", tracks[0])
	}
	if tracks[1].Title != "Two" || tracks[1].ArtistName != "B" || tracks[1].Duration != 2000 {
		t.Errorf("unexpected tracks[1]: %+v", tracks[1])
	}
	if tracks[2].Title != "Three" {
		t.Errorf("unexpected tracks[2]: %+v", tracks[2])
	}
	if !seenStarts["0"] || !seenStarts["2"] {
		t.Errorf("expected pagination to hit starts 0 and 2, got %v", seenStarts)
	}
}

func TestPlaylistTracks_PaginatesWithoutTotalSize(t *testing.T) {
	var starts []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := r.URL.Query().Get("X-Plex-Container-Start")
		starts = append(starts, start)
		w.Header().Set("Content-Type", "application/json")
		switch start {
		case "0", "":
			w.Write([]byte(`{"MediaContainer":{"Metadata":[{"ratingKey":"1","title":"One"}]}}`))
		case "1":
			w.Write([]byte(`{"MediaContainer":{"Metadata":[{"ratingKey":"2","title":"Two"}]}}`))
		case "2":
			w.Write([]byte(`{"MediaContainer":{"Metadata":[]}}`))
		default:
			t.Errorf("unexpected start %q", start)
			w.WriteHeader(http.StatusBadRequest)
		}
	}))
	defer srv.Close()

	tracks, err := newTestClient(srv).PlaylistTracks("42")
	if err != nil {
		t.Fatalf("PlaylistTracks() error: %v", err)
	}
	if len(tracks) != 2 {
		t.Fatalf("expected 2 tracks, got %d", len(tracks))
	}
	wantStarts := []string{"0", "1", "2"}
	if !slices.Equal(starts, wantStarts) {
		t.Fatalf("starts = %v, want %v", starts, wantStarts)
	}
}

// TestPagingStopsWhenServerIgnoresStart covers a server or proxy that drops
// X-Plex-Container-Start. Each page then repeats the first one, and without
// a totalSize no page ends the loop.
func TestPagingStopsWhenServerIgnoresStart(t *testing.T) {
	albums := func(c *Client) (int, error) {
		got, err := c.Albums("7")
		return len(got), err
	}
	playlistTracks := func(c *Client) (int, error) {
		got, err := c.PlaylistTracks("42")
		return len(got), err
	}
	tests := []struct {
		name      string
		fetch     func(c *Client) (int, error)
		page      func(start int) string // the Metadata items for a start
		total     string                 // the totalSize key, or empty to omit it
		wantItems int
		wantErr   string
		wantCalls int
	}{
		{
			name:      "albums repeat the first page",
			fetch:     albums,
			page:      func(int) string { return `{"ratingKey":"1"},{"ratingKey":"2"}` },
			wantErr:   "plex: section 7 albums: server ignored X-Plex-Container-Start 2",
			wantCalls: 2,
		},
		{
			name:      "albums repeat the first page with a totalSize",
			fetch:     albums,
			page:      func(int) string { return `{"ratingKey":"1"},{"ratingKey":"2"}` },
			total:     `"totalSize":10,`,
			wantErr:   "server ignored X-Plex-Container-Start 2",
			wantCalls: 2,
		},
		{
			name:      "albums stop at the page cap",
			fetch:     albums,
			page:      func(start int) string { return fmt.Sprintf(`{"ratingKey":"%d"}`, start) },
			wantItems: maxPages,
			wantCalls: maxPages,
		},
		{
			name:      "playlist items repeat the first page",
			fetch:     playlistTracks,
			page:      func(int) string { return `{"ratingKey":"1","playlistItemID":11},{"ratingKey":"2","playlistItemID":12}` },
			wantErr:   "plex: playlist 42 items: server ignored X-Plex-Container-Start 2",
			wantCalls: 2,
		},
		{
			name:      "playlist items without entry IDs repeat the first page",
			fetch:     playlistTracks,
			page:      func(int) string { return `{"ratingKey":"1"}` },
			wantErr:   "server ignored X-Plex-Container-Start 1",
			wantCalls: 2,
		},
		{
			name:  "one track at the start of two pages",
			fetch: playlistTracks,
			page: func(start int) string {
				if start > 2 {
					return ""
				}
				return fmt.Sprintf(`{"ratingKey":"1","playlistItemID":%d}`, 100+start)
			},
			wantItems: 3,
			wantCalls: 4,
		},
		{
			name:      "playlist items stop at the page cap",
			fetch:     playlistTracks,
			page:      func(start int) string { return fmt.Sprintf(`{"ratingKey":"%d"}`, start) },
			wantItems: maxPages,
			wantCalls: maxPages,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			calls := 0
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				start, _ := strconv.Atoi(r.URL.Query().Get("X-Plex-Container-Start"))
				w.Header().Set("Content-Type", "application/json")
				fmt.Fprintf(w, `{"MediaContainer":{%s"Metadata":[%s]}}`, tt.total, tt.page(start))
			}))
			defer srv.Close()

			n, err := tt.fetch(newTestClient(srv))
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("error = %v, want %q", err, tt.wantErr)
				}
			} else if err != nil {
				t.Fatalf("error = %v", err)
			}
			if n != tt.wantItems {
				t.Errorf("items = %d, want %d", n, tt.wantItems)
			}
			if calls != tt.wantCalls {
				t.Errorf("requests = %d, want %d", calls, tt.wantCalls)
			}
		})
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

func TestClientSendsUserAgent(t *testing.T) {
	var got string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.UserAgent()
		w.Write([]byte(`{"MediaContainer":{"friendlyName":"test"}}`))
	}))
	defer srv.Close()

	if err := NewClient(srv.URL, "tok").Ping(); err != nil {
		t.Fatalf("Ping() error: %v", err)
	}
	if got != httpclient.UserAgent {
		t.Errorf("User-Agent = %q, want %q", got, httpclient.UserAgent)
	}
}

func TestPingRejectsOversizedBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"MediaContainer":{"friendlyName":"` + strings.Repeat("x", maxResponseBody) + `"}}`))
	}))
	defer srv.Close()

	if err := newTestClient(srv).Ping(); !errors.Is(err, httpclient.ErrTooLarge) {
		t.Fatalf("Ping() error = %v, want httpclient.ErrTooLarge", err)
	}
}

func TestAccountClientRediscoversSelectedServer(t *testing.T) {
	var serverRequests atomic.Int32
	media := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		serverRequests.Add(1)
		if got := r.Header.Get("X-Plex-Client-Identifier"); got != "client-1" {
			t.Errorf("server client identifier = %q, want client-1", got)
		}
		if r.URL.Path == "/identity" {
			if got := r.Header.Get("X-Plex-Token"); got != "" {
				t.Errorf("probe token = %q, want none", got)
			}
			identityHandler("server-1")(w, r)
			return
		}
		if got := r.URL.Query().Get("X-Plex-Token"); got != "server-token" {
			t.Errorf("request token = %q, want server-token", got)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"MediaContainer":{"friendlyName":"Home"}}`))
	}))
	defer media.Close()

	var discoveries atomic.Int32
	account := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		discoveries.Add(1)
		if got := r.Header.Get("X-Plex-Client-Identifier"); got != "client-1" {
			t.Errorf("account client identifier = %q, want client-1", got)
		}
		if got := r.Header.Get("X-Plex-Token"); got != "account-token" {
			t.Errorf("account token = %q, want account-token", got)
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `[{"name":"Home","clientIdentifier":"server-1","provides":"server","owned":true,"accessToken":"server-token","connections":[{"uri":%q,"protocol":"http","local":true}]}]`, media.URL)
	}))
	defer account.Close()

	api := &AccountAPI{baseURL: account.URL, client: account.Client(), pollInterval: time.Millisecond}
	client := newAccountClient("http://stale.invalid", "account-token", "client-1", "server-1", api)
	if err := client.Ping(); err != nil {
		t.Fatalf("first Ping: %v", err)
	}
	client.RefreshConnection()
	if err := client.Ping(); err != nil {
		t.Fatalf("second Ping: %v", err)
	}
	if got := discoveries.Load(); got != 2 {
		t.Fatalf("resource discoveries = %d, want 2", got)
	}
	if got := serverRequests.Load(); got != 4 {
		t.Fatalf("server requests = %d, want 4", got)
	}
}

func failoverFixture(t *testing.T, dead *httptest.Server) (*Client, *httptest.Server, *atomic.Int32) {
	t.Helper()
	alive := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/identity":
			w.Write([]byte(`{"MediaContainer":{"machineIdentifier":"server-1"}}`))
		case "/library/sections":
			w.Write([]byte(`{"MediaContainer":{"Directory":[{"key":"3","type":"artist","title":"Music"}]}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(alive.Close)
	discoveries := new(atomic.Int32)
	tv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		discoveries.Add(1)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `[{"name":"Home","clientIdentifier":"server-1","provides":"server","owned":true,"accessToken":"server-token",
			"connections":[{"uri":%q,"protocol":"http","local":true},{"uri":%q,"protocol":"http","local":true}]}]`, dead.URL, alive.URL)
	}))
	t.Cleanup(tv.Close)
	api := &AccountAPI{baseURL: tv.URL, client: tv.Client(), pollInterval: time.Millisecond}
	c := newAccountClient(dead.URL, "account-token", "client-1", "server-1", api)
	// The dead route was the working one when the session found it.
	c.account.connected = true
	c.token = "server-token"
	return c, alive, discoveries
}

func TestClientFailsOverFromDeadRoute(t *testing.T) {
	t.Run("connection refused", func(t *testing.T) {
		dead := httptest.NewServer(http.NotFoundHandler())
		dead.Close()
		c, alive, discoveries := failoverFixture(t, dead)

		sections, err := c.MusicSections()
		if err != nil || len(sections) != 1 {
			t.Fatalf("MusicSections = %v, %v; want the live route's library", sections, err)
		}
		if c.baseURL != alive.URL || discoveries.Load() != 1 {
			t.Fatalf("route = %q after %d discoveries, want %q after 1", c.baseURL, discoveries.Load(), alive.URL)
		}
	})

	t.Run("no response", func(t *testing.T) {
		old := responseHeaderTimeout
		responseHeaderTimeout = 50 * time.Millisecond
		t.Cleanup(func() { responseHeaderTimeout = old })
		dead := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			<-r.Context().Done()
		}))
		t.Cleanup(dead.Close)
		c, alive, _ := failoverFixture(t, dead)

		if _, err := doWithHeaderDeadline(http.DefaultClient, mustRequest(t, dead.URL)); err == nil || err.Error() != "no response in 50ms" {
			t.Fatalf("hanging route error = %v, want no response in 50ms", err)
		}
		start := time.Now()
		if _, err := c.MusicSections(); err != nil {
			t.Fatalf("MusicSections: %v", err)
		}
		if c.baseURL != alive.URL {
			t.Fatalf("route = %q, want %q", c.baseURL, alive.URL)
		}
		if elapsed := time.Since(start); elapsed > 5*time.Second {
			t.Fatalf("failover took %v", elapsed)
		}
	})
}

func TestClientKeepsRouteOnHTTPError(t *testing.T) {
	failing := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	t.Cleanup(failing.Close)
	c, _, discoveries := failoverFixture(t, failing)

	if _, err := c.MusicSections(); err == nil || !strings.Contains(err.Error(), "500") {
		t.Fatalf("MusicSections error = %v, want the server's HTTP 500", err)
	}
	if discoveries.Load() != 0 || c.baseURL != failing.URL {
		t.Fatalf("an HTTP error rediscovered the route: %d discoveries, route %q", discoveries.Load(), c.baseURL)
	}
}

func TestManualClientDoesNotFailOver(t *testing.T) {
	dead := httptest.NewServer(http.NotFoundHandler())
	dead.Close()
	_, err := NewClient(dead.URL, "secret-token").MusicSections()
	if err == nil || !strings.Contains(err.Error(), "server unreachable") {
		t.Fatalf("MusicSections error = %v, want server unreachable", err)
	}
	if strings.Contains(err.Error(), "secret-token") {
		t.Fatalf("MusicSections error = %v names the token", err)
	}
}

func mustRequest(t *testing.T, target string) *http.Request {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, target, nil)
	if err != nil {
		t.Fatal(err)
	}
	return req
}

// A route that died while a track played is found before the next track:
// ResolveSource builds its URL on the route that answers.
func TestResolveSourceMovesOffDeadRoute(t *testing.T) {
	dead := httptest.NewServer(http.NotFoundHandler())
	dead.Close()
	c, alive, discoveries := failoverFixture(t, dead)
	p := newProvider(c)
	p.partKeys = map[string]string{"200": "/library/parts/1/111/file.flac"}

	got, err := p.ResolveSource(TrackURIPrefix + "200")
	if err != nil {
		t.Fatalf("ResolveSource: %v", err)
	}
	if !strings.HasPrefix(got, alive.URL+"/library/parts/1/111/file.flac?") || discoveries.Load() != 1 {
		t.Fatalf("ResolveSource = %q after %d discoveries, want the live route after 1", got, discoveries.Load())
	}
}

// A manual client checks no route before a track.
func TestManualResolveSourceSendsNoRequest(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("unexpected request %s", r.URL.Path)
	}))
	defer srv.Close()
	p := newProvider(NewClient(srv.URL, "tok"))
	p.partKeys = map[string]string{"200": "/library/parts/1/111/file.flac"}
	if _, err := p.ResolveSource(TrackURIPrefix + "200"); err != nil {
		t.Fatalf("ResolveSource: %v", err)
	}
}

// After a network change the cached local address can belong to another
// Plex server. The check before a track sends it no token and moves to the
// route that answers as the selected server.
func TestResolveSourceChecksIdentityWithoutToken(t *testing.T) {
	stranger := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("X-Plex-Token") + r.Header.Get("X-Plex-Token"); got != "" {
			t.Errorf("other server got token %q on %s", got, r.URL.Path)
		}
		identityHandler("other-server")(w, r)
	}))
	t.Cleanup(stranger.Close)
	c, alive, discoveries := failoverFixture(t, stranger)
	p := newProvider(c)
	p.partKeys = map[string]string{"200": "/library/parts/1/111/file.flac"}

	got, err := p.ResolveSource(TrackURIPrefix + "200")
	if err != nil {
		t.Fatalf("ResolveSource: %v", err)
	}
	if !strings.HasPrefix(got, alive.URL+"/library/parts/1/111/file.flac?") || discoveries.Load() != 1 {
		t.Fatalf("ResolveSource = %q after %d discoveries, want the selected server after 1", got, discoveries.Load())
	}
}

// When the retry fails too, its route is dropped as well, so the next
// request rediscovers instead of waiting on a known dead route.
func TestClientDropsRouteWhenRetryFails(t *testing.T) {
	dead := httptest.NewServer(http.NotFoundHandler())
	dead.Close()
	flaky := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/identity" {
			identityHandler("server-1")(w, r)
			return
		}
		if conn, _, err := w.(http.Hijacker).Hijack(); err == nil {
			conn.Close()
		}
	}))
	t.Cleanup(flaky.Close)
	tv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `[{"name":"Home","clientIdentifier":"server-1","provides":"server","owned":true,"accessToken":"server-token",
			"connections":[{"uri":%q,"protocol":"http","local":true}]}]`, flaky.URL)
	}))
	t.Cleanup(tv.Close)
	api := &AccountAPI{baseURL: tv.URL, client: tv.Client(), pollInterval: time.Millisecond}
	c := newAccountClient(dead.URL, "account-token", "client-1", "server-1", api)
	c.account.connected = true

	if _, err := c.MusicSections(); err == nil {
		t.Fatal("MusicSections: want an error from the failing retry")
	}
	if c.account.connected {
		t.Fatalf("route %q stays connected after the retry failed", c.baseURL)
	}
}

// A manual client has no other route, so a slow server keeps the full API
// timeout instead of the account client's header deadline.
func TestManualClientWaitsPastHeaderDeadline(t *testing.T) {
	old := responseHeaderTimeout
	responseHeaderTimeout = 20 * time.Millisecond
	t.Cleanup(func() { responseHeaderTimeout = old })
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(100 * time.Millisecond)
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"MediaContainer":{"friendlyName":"Home"}}`))
	}))
	defer srv.Close()
	if err := NewClient(srv.URL, "tok").Ping(); err != nil {
		t.Fatalf("Ping: %v", err)
	}
}

// lateTransport returns a response after the header deadline has fired,
// ignoring the cancel, as a response that races the timer would.
type lateTransport struct{ delay time.Duration }

func (lt lateTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	time.Sleep(lt.delay)
	return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader("{}")), Request: req}, nil
}

// A response that arrives after the deadline fired is not returned: its
// body could already be cancelled.
func TestHeaderDeadlineWinsOverLateResponse(t *testing.T) {
	old := responseHeaderTimeout
	responseHeaderTimeout = 10 * time.Millisecond
	t.Cleanup(func() { responseHeaderTimeout = old })
	client := &http.Client{Transport: lateTransport{delay: 50 * time.Millisecond}}
	resp, err := doWithHeaderDeadline(client, mustRequest(t, "http://plex.invalid/"))
	if err == nil {
		resp.Body.Close()
		t.Fatal("a response after the deadline was returned")
	}
	if err.Error() != "no response in 10ms" {
		t.Fatalf("error = %v, want no response in 10ms", err)
	}
}
