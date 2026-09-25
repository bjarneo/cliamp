package model

import (
	"context"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/bjarneo/cliamp/playlist"
	"github.com/bjarneo/cliamp/provider"
)

func spotSearchAllFixture() provider.SearchResults {
	return provider.SearchResults{
		Tracks: []playlist.Track{
			{Path: "spotify:track:0", Title: "Dreams", Artist: "Fleetwood Mac"},
			{Path: "spotify:track:1", Title: "Go Your Own Way", Artist: "Fleetwood Mac"},
		},
		Albums: []provider.AlbumInfo{{ID: "al1", Name: "Rumours", Artist: "Fleetwood Mac", Year: 1977}},
		Artists: []provider.ArtistInfo{
			{ID: "ar1", Name: "Fleetwood Mac", AlbumCount: 0},
			{ID: "ar2", Name: "ABBA", AlbumCount: 9},
		},
		Playlists: []playlist.PlaylistInfo{{ID: "pl1", Name: "Classic Hits", TrackCount: 42}},
	}
}

// openSpotMultiSearch drives the multi-search flow through the input screen
// and applies the resulting message.
func openSpotMultiSearch(t *testing.T, m Model) Model {
	t.Helper()
	m.openProviderSearchWith(m.provider)
	m.spotSearch.query = "fleet"
	updated, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = updated.(Model)
	if cmd == nil {
		t.Fatal("Enter returned nil search command")
	}
	msg, ok := cmd().(spotSearchAllMsg)
	if !ok {
		t.Fatalf("search produced %T; want spotSearchAllMsg", cmd())
	}
	updated, _ = m.Update(msg)
	m = updated.(Model)
	if m.spotSearch.screen != spotSearchResults || !m.spotSearch.multi {
		t.Fatalf("screen = %d multi = %v; want tabbed results", m.spotSearch.screen, m.spotSearch.multi)
	}
	return m
}

func TestSpotSearchTabSwitching(t *testing.T) {
	fake := &fakeSpotifyLibProvider{name: "Spotify", searchAll: spotSearchAllFixture()}
	m := newSpotifyTestModel(fake)
	m = openSpotMultiSearch(t, m)

	wantTabs := []struct {
		key  tea.KeyPressMsg
		tab  spotSearchTab
		name string
	}{
		{tea.KeyPressMsg{Code: tea.KeyRight}, spotTabAlbums, "right"},
		{tea.KeyPressMsg{Code: tea.KeyTab}, spotTabArtists, "tab"},
		{tea.KeyPressMsg{Code: tea.KeyTab, Mod: tea.ModShift}, spotTabAlbums, "shift+tab steps back"},
		{tea.KeyPressMsg{Code: tea.KeyTab, Mod: tea.ModShift}, spotTabTracks, "shift+tab to tracks"},
		{tea.KeyPressMsg{Code: tea.KeyTab, Mod: tea.ModShift}, spotTabPlaylists, "shift+tab wraps around"},
	}

	for _, tt := range wantTabs {
		updated, _ := m.Update(tt.key)
		m = updated.(Model)
		if m.spotSearch.tab != tt.tab {
			t.Fatalf("%s: tab = %d, want %d", tt.name, m.spotSearch.tab, tt.tab)
		}
		if m.spotSearch.cursor != 0 {
			t.Fatalf("%s: cursor = %d, want reset to 0", tt.name, m.spotSearch.cursor)
		}
	}

	// The tab bar renders every tab label with its count.
	m.plVisible = 12
	body := m.renderSpotSearchBody()
	for _, label := range []string{"Tracks (2)", "Albums (1)", "Artists (2)", "Playlists (1)"} {
		if !strings.Contains(body, label) {
			t.Errorf("tab bar %q missing %q", body, label)
		}
	}
}

func TestSpotSearchTrackTabKeepsRowActions(t *testing.T) {
	fake := &fakeSpotifyLibProvider{name: "Spotify", searchAll: spotSearchAllFixture()}
	m := newSpotifyTestModel(fake)
	m = openSpotMultiSearch(t, m)

	// Move to the second track and queue it next ("q" closes and queues).
	updated, _ := m.Update(tea.KeyPressMsg{Text: "j"})
	m = updated.(Model)
	updated, _ = m.Update(tea.KeyPressMsg{Text: "q"})
	m = updated.(Model)
	if m.spotSearch.visible {
		t.Fatal("q should close the overlay")
	}
	if m.playlist.Len() != 1 || m.playlist.Tracks()[0].Title != "Go Your Own Way" {
		t.Fatalf("queue = %+v; want queued second result", m.playlist.Tracks())
	}
}

func TestSpotSearchAlbumDrillDown(t *testing.T) {
	fake := &fakeSpotifyLibProvider{name: "Spotify", searchAll: spotSearchAllFixture()}
	fake.albumTracks = map[string][]playlist.Track{
		"al1": {{Path: "spotify:track:9", Title: "The Chain", Artist: "Fleetwood Mac"}},
	}
	m := newSpotifyTestModel(fake)
	m = openSpotMultiSearch(t, m)

	updated, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyRight})
	m = updated.(Model)
	updated, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = updated.(Model)
	if _, ok := cmd().(spotDrillLoadedMsg); !ok {
		t.Fatalf("enter on album produced %T", cmd())
	}
	if len(m.spotSearch.drill) != 1 || m.spotSearch.drill[0].crumb != "Album — Rumours" {
		t.Fatalf("drill = %+v; want Album — Rumours level", m.spotSearch.drill)
	}
	msg := cmd().(spotDrillLoadedMsg)
	updated, _ = m.Update(msg)
	m = updated.(Model)
	lvl := m.spotSearch.drill[0]
	if len(lvl.tracks) != 1 || lvl.tracks[0].Title != "The Chain" {
		t.Fatalf("drill tracks = %+v", lvl.tracks)
	}

	// Drill bodies render the crumb.
	m.plVisible = 12
	if body := m.renderSpotSearchBody(); !strings.Contains(body, "Album — Rumours") {
		t.Fatalf("drill body %q missing crumb", body)
	}

	// Esc pops back to the tab bar with the previous tab and cursor intact.
	updated, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	m = updated.(Model)
	if len(m.spotSearch.drill) != 0 {
		t.Fatal("Esc should leave the drill stack")
	}
	if m.spotSearch.tab != spotTabAlbums || m.spotSearch.cursor != 0 {
		t.Fatalf("tab = %d cursor = %d; want restored albums tab", m.spotSearch.tab, m.spotSearch.cursor)
	}
}

// albumOnlyProvider implements MultiSearcher + ArtistBrowser +
// AlbumTrackLoader, exercising the artist drill's album-list path.
type albumOnlyProvider struct {
	commandsTestProvider
	searchAll    provider.SearchResults
	artistsList  []provider.ArtistInfo
	artistAlbums map[string][]provider.AlbumInfo
	albumTracks  map[string][]playlist.Track
}

func (p *albumOnlyProvider) SearchAll(context.Context, string, int) (provider.SearchResults, error) {
	return p.searchAll, nil
}

func (p *albumOnlyProvider) Artists() ([]provider.ArtistInfo, error) {
	return p.artistsList, nil
}

func (p *albumOnlyProvider) ArtistAlbums(artistID string) ([]provider.AlbumInfo, error) {
	return p.artistAlbums[artistID], nil
}

func (p *albumOnlyProvider) AlbumTracks(albumID string) ([]playlist.Track, error) {
	return p.albumTracks[albumID], nil
}

func TestSpotSearchArtistDrillLoadsAlbums(t *testing.T) {
	sp := &albumOnlyProvider{
		commandsTestProvider: commandsTestProvider{name: "Spotify"},
		searchAll:            spotSearchAllFixture(),
		artistAlbums: map[string][]provider.AlbumInfo{
			"ar1": {{ID: "al2", Name: "Rumours", Artist: "Fleetwood Mac", Year: 1977}},
		},
		albumTracks: map[string][]playlist.Track{
			"al2": {{Path: "spotify:track:3", Title: "Second Hand News", Artist: "Fleetwood Mac"}},
		},
	}
	fake := &fakeSpotifyLibProvider{name: "Spotify"}
	m := newSpotifyTestModel(fake)
	m.provider = sp
	m.providers = []ProviderEntry{{Key: "spotify", Name: "Spotify", Provider: sp}}
	m = openSpotMultiSearch(t, m)

	// Artists tab, enter → album list level.
	updated, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyRight})
	m = updated.(Model)
	updated, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyTab})
	m = updated.(Model)
	updated, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = updated.(Model)
	msg := cmd().(spotDrillLoadedMsg)
	updated, _ = m.Update(msg)
	m = updated.(Model)
	if len(m.spotSearch.drill) != 1 || len(m.spotSearch.drill[0].albums) != 1 {
		t.Fatalf("drill = %+v; want artist album list", m.spotSearch.drill)
	}

	// Enter on the album drills one level deeper into tracks.
	updated, cmd = m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = updated.(Model)
	msg2, ok := cmd().(spotDrillLoadedMsg)
	if !ok {
		t.Fatalf("enter on album produced %T", cmd())
	}
	updated, _ = m.Update(msg2)
	m = updated.(Model)
	if len(m.spotSearch.drill) != 2 || len(m.spotSearch.drill[1].tracks) != 1 {
		t.Fatalf("drill = %+v; want nested album tracks", m.spotSearch.drill)
	}
	if m.spotDrillCrumb() != "Artist — Fleetwood Mac / Album — Rumours" {
		t.Fatalf("crumb = %q", m.spotDrillCrumb())
	}

	// One Esc returns to the artist's album list, not the tabs.
	updated, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	m = updated.(Model)
	if len(m.spotSearch.drill) != 1 || m.spotSearch.drill[0].albums == nil {
		t.Fatalf("drill = %+v; want album list after one Esc", m.spotSearch.drill)
	}
}

// TestSpotSearchPlaylistDrillUsesTracks pins the drill path to Tracks(), not
// TrackPager: a pager read would rewrite the provider's page cursor for the
// playlist ID and corrupt an incremental queue load of it in flight.
func TestSpotSearchPlaylistDrillUsesTracks(t *testing.T) {
	fake := &fakeSpotifyLibProvider{name: "Spotify", searchAll: spotSearchAllFixture()}
	fake.allTracks = spotifyTracks(3)
	m := newSpotifyTestModel(fake)
	m = openSpotMultiSearch(t, m)

	for range 3 {
		updated, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyRight})
		m = updated.(Model)
	}
	if m.spotSearch.tab != spotTabPlaylists {
		t.Fatalf("tab = %d, want playlists", m.spotSearch.tab)
	}
	updated, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = updated.(Model)
	msg, ok := cmd().(spotDrillLoadedMsg)
	if !ok {
		t.Fatalf("enter on playlist produced %T", cmd())
	}
	if len(fake.pageCalls) != 0 {
		t.Fatalf("pageCalls = %v; want no pager reads from the drill path", fake.pageCalls)
	}
	updated, _ = m.Update(msg)
	m = updated.(Model)
	if len(m.spotSearch.drill) != 1 || len(m.spotSearch.drill[0].tracks) != 3 {
		t.Fatalf("drill = %+v; want playlist tracks", m.spotSearch.drill)
	}
}

func TestSpotSearchFollowPlaylistFromTab(t *testing.T) {
	fake := &fakeSpotifyLibProvider{name: "Spotify", searchAll: spotSearchAllFixture()}
	fake.lists = []playlist.PlaylistInfo{{ID: "pl1", Name: "Classic Hits"}}
	m := newSpotifyTestModel(fake)
	m.providerLists = fake.lists
	m = openSpotMultiSearch(t, m)

	for range 3 {
		updated, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyRight})
		m = updated.(Model)
	}
	updated, cmd := m.Update(tea.KeyPressMsg{Text: "f"})
	m = updated.(Model)
	msg, ok := cmd().(playlistFollowedMsg)
	if !ok || !msg.follow {
		t.Fatalf("f produced %T %+v; want follow", cmd(), msg)
	}
	updated, _ = m.Update(msg)
	m = updated.(Model)
	if len(fake.followedPlaylists) != 1 || fake.followedPlaylists[0] != "pl1" {
		t.Fatalf("followedPlaylists = %v", fake.followedPlaylists)
	}
	if !strings.Contains(m.status.text, "Followed playlist") {
		t.Fatalf("status = %q", m.status.text)
	}

	// Second press unfollows via the session-local toggle.
	updated, cmd = m.Update(tea.KeyPressMsg{Text: "f"})
	m = updated.(Model)
	msg, ok = cmd().(playlistFollowedMsg)
	if !ok || msg.follow {
		t.Fatalf("second f produced %T %+v; want unfollow", cmd(), msg)
	}
	updated, _ = m.Update(msg)
	m = updated.(Model)
	if len(fake.unfollowedPlaylists) != 1 || fake.unfollowedPlaylists[0] != "pl1" {
		t.Fatalf("unfollowedPlaylists = %v", fake.unfollowedPlaylists)
	}
}

func TestSpotSearchLikeFromTrackTab(t *testing.T) {
	fake := &fakeSpotifyLibProvider{name: "Spotify", searchAll: spotSearchAllFixture()}
	m := newSpotifyTestModel(fake)
	m = openSpotMultiSearch(t, m)

	updated, cmd := m.Update(tea.KeyPressMsg{Text: "S"})
	m = updated.(Model)
	msg, ok := cmd().(trackLikeToggledMsg)
	if !ok {
		t.Fatalf("S produced %T", cmd())
	}
	updated, _ = m.Update(msg)
	m = updated.(Model)
	if len(fake.liked) != 1 || fake.liked[0] != "spotify:track:0" {
		t.Fatalf("liked = %v", fake.liked)
	}
	if !m.spotSearch.visible {
		t.Fatal("like should not close the search overlay")
	}
}

func TestSpotSearchEmptyResults(t *testing.T) {
	fake := &fakeSpotifyLibProvider{name: "Spotify"}
	m := newSpotifyTestModel(fake)
	m = openSpotMultiSearch(t, m)
	if m.spotSearch.err != "No results found" {
		t.Fatalf("err = %q, want no-results message", m.spotSearch.err)
	}
}
