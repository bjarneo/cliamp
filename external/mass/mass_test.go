package mass

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/bjarneo/cliamp/config"
)

// fakeToken is a synthetic bearer token. No real token is used in tests.
const fakeToken = "test-token-not-a-real-secret"

type call struct {
	Command string
	Args    map[string]any
}

// fakeMA answers Music Assistant JSON-RPC commands from a table of canned
// results and records every call.
type fakeMA struct {
	srv     *httptest.Server
	mu      sync.Mutex
	calls   []call
	bearer  string
	results map[string]any
}

func newFakeMA(t *testing.T, results map[string]any) *fakeMA {
	t.Helper()
	f := &fakeMA{results: results}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api" {
			http.NotFound(w, r)
			return
		}
		var req struct {
			Command string         `json:"command"`
			Args    map[string]any `json:"args"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "bad json", http.StatusBadRequest)
			return
		}
		f.mu.Lock()
		f.bearer = r.Header.Get("Authorization")
		f.calls = append(f.calls, call{req.Command, req.Args})
		f.mu.Unlock()
		res, ok := f.results[req.Command]
		if !ok {
			http.Error(w, "Invalid Command: "+req.Command, http.StatusBadRequest)
			return
		}
		if fn, ok := res.(func(args map[string]any) any); ok {
			res = fn(req.Args)
		}
		_ = json.NewEncoder(w).Encode(res)
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeMA) provider() *Provider {
	return NewFromConfig(config.MassConfig{URL: f.srv.URL + "/", Token: fakeToken})
}

func (f *fakeMA) last() call {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.calls) == 0 {
		return call{}
	}
	return f.calls[len(f.calls)-1]
}

func TestNewFromConfigNeedsURLAndToken(t *testing.T) {
	if NewFromConfig(config.MassConfig{Token: fakeToken}) != nil {
		t.Error("provider without a URL")
	}
	if NewFromConfig(config.MassConfig{URL: "http://ma:8095"}) != nil {
		t.Error("provider without a token")
	}
}

func TestPlaylistsListsFoldersThenPlaylists(t *testing.T) {
	f := newFakeMA(t, map[string]any{
		cmdLibraryPlaylists: []map[string]any{
			{"uri": "spotify://playlist/abc", "name": "Chill Mix", "track_count": 12},
			{"uri": "library://playlist/7", "name": ""},
			{"uri": "library://playlist/42", "name": "Workout"},
		},
	})
	infos, err := f.provider().Playlists()
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, in := range infos {
		names = append(names, in.Name)
	}
	want := []string{"Recently played", "Artists", "Albums", "Tracks", "Genres", "Radio stations", "Chill Mix", "Workout"}
	if !reflect.DeepEqual(names, want) {
		t.Fatalf("names = %q, want %q", names, want)
	}
	if got := infos[6]; got.ID != "spotify://playlist/abc" || got.TrackCount != 12 {
		t.Errorf("playlist row = %+v", got)
	}
	if f.bearer != "Bearer "+fakeToken {
		t.Error("request did not carry the bearer token")
	}
}

func TestTracksOfPlaylist(t *testing.T) {
	f := newFakeMA(t, map[string]any{
		cmdPlaylistTracks: []map[string]any{{
			"uri": "spotify://track/t1", "name": "Song One", "duration": 180, "track_number": 3,
			"artists": []map[string]any{{"name": "A"}, {"name": "B"}},
			"album":   map[string]any{"name": "Record"},
		}},
	})
	tracks, err := f.provider().Tracks("spotify://playlist/abc")
	if err != nil {
		t.Fatal(err)
	}
	if len(tracks) != 1 {
		t.Fatalf("got %d tracks", len(tracks))
	}
	tr := tracks[0]
	if tr.Path != "mass://spotify://track/t1" || !IsTrackPath(tr.Path) {
		t.Errorf("Path = %q", tr.Path)
	}
	if tr.ProviderMeta[MetaURI] != "spotify://track/t1" {
		t.Errorf("MetaURI = %q", tr.ProviderMeta[MetaURI])
	}
	if tr.Title != "Song One" || tr.Artist != "A, B" || tr.Album != "Record" || tr.DurationSecs != 180 || tr.TrackNumber != 3 {
		t.Errorf("track = %+v", tr)
	}
	if tr.Stream {
		t.Error("a Music Assistant row must not be marked as an HTTP stream")
	}
	args := f.last().Args
	if args["item_id"] != "abc" || args["provider_instance_id_or_domain"] != "spotify" {
		t.Errorf("args = %v", args)
	}
}

func TestTracksRejectsUnplayableURI(t *testing.T) {
	f := newFakeMA(t, nil)
	if _, err := f.provider().Tracks("mass://library://radio/9"); err == nil {
		t.Fatal("want error for a radio URI")
	}
}

func TestFolderRowsSkipFolders(t *testing.T) {
	f := newFakeMA(t, map[string]any{
		"music/recently_played_items": []map[string]any{
			{"uri": "library://album/1", "name": "Album", "media_type": "album", "artists": []map[string]any{{"name": "Band"}}},
			{"uri": "library://folder/2", "name": "Sub", "media_type": "folder"},
			{"uri": "", "name": "No URI", "media_type": "track"},
		},
	})
	rows, err := f.provider().Tracks(folderPrefix + "recent")
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].Title != "Album" || rows[0].Artist != "Band" || rows[0].ProviderMeta[MetaURI] != "library://album/1" {
		t.Fatalf("rows = %+v", rows)
	}
	if f.last().Args["user_initiated_only"] != true {
		t.Errorf("recently played args = %v", f.last().Args)
	}
	if _, err := f.provider().Tracks(folderPrefix + "nope"); err == nil {
		t.Error("want error for an unknown folder")
	}
}

func TestSearchTracks(t *testing.T) {
	f := newFakeMA(t, map[string]any{cmdSearch: map[string]any{"tracks": []map[string]any{
		{"uri": "library://track/1", "name": "Found"},
		{"uri": "library://track/2", "name": ""},
	}}})
	got, err := f.provider().SearchTracks(context.Background(), "q", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Title != "Found" || !IsTrackPath(got[0].Path) {
		t.Fatalf("tracks = %+v", got)
	}
	if args := f.last().Args; args["search_query"] != "q" || args["limit"] != float64(25) {
		t.Errorf("args = %v", args)
	}
}

func TestAlbumListAndTracks(t *testing.T) {
	f := newFakeMA(t, map[string]any{
		cmdLibraryAlbums: []map[string]any{{"uri": "library://album/9", "name": "Abbey Road", "year": 1969,
			"artists": []map[string]any{{"name": "The Beatles"}}, "track_count": 17}},
		cmdAlbumTracks: []map[string]any{{"uri": "library://track/5", "name": "Come Together"}},
	})
	p := f.provider()
	albums, err := p.AlbumList("default", 0, 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(albums) != 1 || albums[0].Artist != "The Beatles" || albums[0].Year != 1969 || albums[0].TrackCount != 17 {
		t.Fatalf("albums = %+v", albums)
	}
	tracks, err := p.AlbumTracks(albums[0].ID)
	if err != nil || len(tracks) != 1 || tracks[0].Title != "Come Together" {
		t.Fatalf("album tracks = %+v, %v", tracks, err)
	}
}

func TestAPIErrorIncludesStatus(t *testing.T) {
	f := newFakeMA(t, nil)
	_, err := f.provider().Playlists()
	if err == nil || !strings.Contains(err.Error(), "400") {
		t.Fatalf("err = %v", err)
	}
}

func TestSendspinPlayerID(t *testing.T) {
	players := []map[string]any{
		{"player_id": "up1", "output_protocols": []map[string]any{{"output_protocol_id": "other", "protocol_domain": "sendspin"}}},
		{"player_id": "up2", "output_protocols": []map[string]any{{"output_protocol_id": "CLIAMP-ABC", "protocol_domain": "sendspin"}}},
		{"player_id": "cliamp-direct"},
	}
	f := newFakeMA(t, map[string]any{"players/all": players})
	p := f.provider()
	ctx := context.Background()

	if id, err := p.SendspinPlayerID(ctx, "cliamp-abc"); err != nil || id != "up2" {
		t.Errorf("wrapped player: id=%q err=%v", id, err)
	}
	if id, err := p.SendspinPlayerID(ctx, "cliamp-direct"); err != nil || id != "cliamp-direct" {
		t.Errorf("direct player: id=%q err=%v", id, err)
	}
	if _, err := p.SendspinPlayerID(ctx, "missing"); !errors.Is(err, ErrPlayerNotFound) {
		t.Errorf("missing player: err=%v", err)
	}
}

func TestPlayersOrderAndGroups(t *testing.T) {
	f := newFakeMA(t, map[string]any{"players/all": []map[string]any{
		{"player_id": "off", "display_name": "Off", "available": false},
		{"player_id": "lead", "display_name": "Kitchen", "available": true, "playback_state": "playing",
			"group_members": []string{"lead", "kid"}, "supported_features": []string{"set_members"}},
		{"player_id": "hidden", "display_name": "Hidden", "available": true, "hide_in_ui": true},
		{"player_id": "kid", "name": "Den", "available": true, "synced_to": "lead"},
	}})
	got, err := f.provider().Players(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, pl := range got {
		ids = append(ids, pl.ID)
	}
	if !reflect.DeepEqual(ids, []string{"lead", "kid", "off"}) {
		t.Fatalf("order = %v", ids)
	}
	lead, kid := got[0], got[1]
	if !lead.CanGroup || !lead.Grouped() || !reflect.DeepEqual(lead.Members, []string{"kid"}) || lead.State != "playing" {
		t.Errorf("leader = %+v", lead)
	}
	if kid.Name != "Den" || !kid.Grouped() || kid.CanGroup {
		t.Errorf("follower = %+v", kid)
	}
}

func TestPlayerCommandsMatchMusicAssistant(t *testing.T) {
	f := newFakeMA(t, map[string]any{
		"player_queues/play_media": nil, "player_queues/pause": nil, "player_queues/play": nil,
		"player_queues/next": nil, "player_queues/previous": nil,
		"players/cmd/set_members": nil, "players/cmd/ungroup": nil,
	})
	p := f.provider()
	ctx := context.Background()
	cases := []struct {
		run  func() error
		want call
	}{
		{func() error { return p.PlayMedia(ctx, "q", []string{"a", "b"}) },
			call{"player_queues/play_media", map[string]any{"queue_id": "q", "media": []any{"a", "b"}, "option": "replace"}}},
		{func() error { return p.Pause(ctx, "q") }, call{"player_queues/pause", map[string]any{"queue_id": "q"}}},
		{func() error { return p.Resume(ctx, "q") }, call{"player_queues/play", map[string]any{"queue_id": "q"}}},
		{func() error { return p.Next(ctx, "q") }, call{"player_queues/next", map[string]any{"queue_id": "q"}}},
		{func() error { return p.Previous(ctx, "q") }, call{"player_queues/previous", map[string]any{"queue_id": "q"}}},
		{func() error { return p.Join(ctx, "lead", "me") },
			call{"players/cmd/set_members", map[string]any{"target_player": "lead", "player_ids_to_add": []any{"me"}}}},
		{func() error { return p.Leave(ctx, "me") }, call{"players/cmd/ungroup", map[string]any{"player_id": "me"}}},
	}
	for _, c := range cases {
		if err := c.run(); err != nil {
			t.Fatalf("%s: %v", c.want.Command, err)
		}
		if got := f.last(); !reflect.DeepEqual(got, c.want) {
			t.Errorf("sent %+v, want %+v", got, c.want)
		}
	}
}

// rows returns n library track rows numbered from first.
func rows(first, n int) []map[string]any {
	out := make([]map[string]any, n)
	for i := range out {
		id := first + i
		out[i] = map[string]any{"uri": fmt.Sprintf("library://track/%d", id), "name": fmt.Sprintf("T%d", id), "media_type": "track", "duration": 59.8}
	}
	return out
}

// A folder larger than one page loads page by page until a short page.
func TestFolderLoadsEveryPage(t *testing.T) {
	f := newFakeMA(t, map[string]any{"music/tracks/library_items": func(args map[string]any) any {
		switch args["offset"] {
		case float64(0):
			return rows(0, folderPageSize)
		case float64(folderPageSize):
			return rows(folderPageSize, 7)
		}
		return []map[string]any{}
	}})
	p := f.provider()

	first, next, err := p.TracksPage(folderPrefix+"tracks", 0)
	if err != nil || len(first) != folderPageSize || next != folderPageSize {
		t.Fatalf("page 1: %d rows, next %d, %v", len(first), next, err)
	}
	if first[0].DurationSecs != 60 {
		t.Errorf("folder track row lost its length: %+v", first[0])
	}
	second, next, err := p.TracksPage(folderPrefix+"tracks", next)
	if err != nil || len(second) != 7 || next != 0 || second[6].Title != fmt.Sprintf("T%d", folderPageSize+6) {
		t.Fatalf("page 2: %d rows, next %d, %v", len(second), next, err)
	}

	all, err := p.Tracks(folderPrefix + "tracks")
	if err != nil || len(all) != folderPageSize+7 {
		t.Fatalf("Tracks = %d rows, %v; want every page", len(all), err)
	}
}

// A search replaces the library rows with the results until it is cleared.
func TestSearchCatalogReplacesLibraryUntilCleared(t *testing.T) {
	f := newFakeMA(t, map[string]any{
		cmdLibraryPlaylists: []map[string]any{{"uri": "library://playlist/1", "name": "Mine"}},
		cmdSearch: map[string]any{
			"tracks":    rows(1, 3),
			"artists":   []map[string]any{{"uri": "library://artist/9", "name": "Jimi Hendrix"}},
			"albums":    []map[string]any{{"uri": "library://album/4", "name": "Axis", "artists": []map[string]any{{"name": "Jimi Hendrix"}}, "track_count": 13}},
			"playlists": []map[string]any{},
		},
	})
	p := f.provider()

	n, err := p.SearchCatalog("hendrix")
	if err != nil || n != 3 || !p.IsSearching() {
		t.Fatalf("SearchCatalog = %d, %v; searching %v", n, err, p.IsSearching())
	}
	if args := f.last().Args; args["search_query"] != "hendrix" || args["library_only"] != true {
		t.Errorf("search args = %v", args)
	}
	lists, _ := p.Playlists()
	var got []string
	for _, l := range lists {
		got = append(got, l.Section+": "+l.Name)
	}
	want := []string{"Tracks: All 3 matching tracks", "Artists: Jimi Hendrix", "Albums: Axis — Jimi Hendrix"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("results = %q, want %q", got, want)
	}
	tracks, err := p.Tracks(lists[0].ID)
	if err != nil || len(tracks) != 3 || !IsTrackPath(tracks[0].Path) {
		t.Fatalf("matching tracks = %+v, %v", tracks, err)
	}

	p.ClearSearch()
	lists, _ = p.Playlists()
	if p.IsSearching() || lists[0].Name != "Recently played" || lists[len(lists)-1].Name != "Mine" {
		t.Errorf("after ClearSearch: searching %v, rows %+v", p.IsSearching(), lists)
	}
}
