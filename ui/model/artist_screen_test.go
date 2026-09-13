package model

import (
	"errors"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/bjarneo/cliamp/playlist"
	"github.com/bjarneo/cliamp/provider"
	"github.com/bjarneo/cliamp/ui"
)

// artist_screen_test.go covers the artist profile overlay against the frozen
// ArtistDetailLoader contract, using the shared fakeSpotifyProvider plumbing.

// ArtistDetail makes fakeSpotifyProvider implement provider.ArtistDetailLoader
// (the fake ArtistFollower — FollowArtist/UnfollowArtist — already exists).
// A nil map keeps the fake OFF the contract so older drills stay on their
// fallback paths; a missing entry simulates a load error.
func (f *fakeSpotifyProvider) ArtistDetail(artistID string) (provider.ArtistDetail, error) {
	if f.artistDetails == nil {
		return provider.ArtistDetail{}, errors.New("artist detail unavailable")
	}
	detail, ok := f.artistDetails[artistID]
	if !ok {
		return provider.ArtistDetail{}, errors.New("artist detail unavailable")
	}
	return detail, f.err
}

func artistFixtureArtist() provider.ArtistInfo {
	return provider.ArtistInfo{ID: "ar1", Name: "Fleetwood Mac", AlbumCount: 2}
}

// artistDetailFixture popular pool differs by popularity, year, and liked
// marks so every sort mode produces a distinct order.
func artistDetailFixture() provider.ArtistDetail {
	meta := func(pop, liked string) map[string]string {
		m := map[string]string{provider.MetaSpotifyPopularity: pop}
		if liked != "" {
			m[provider.MetaSpotifyLiked] = liked
		}
		return m
	}
	return provider.ArtistDetail{
		Info:      artistFixtureArtist(),
		Genres:    []string{"rock", "classic rock"},
		Followers: 1234567,
		Popular: []playlist.Track{
			{Path: "spotify:track:1", Title: "Dreams", Artist: "Fleetwood Mac", Year: 1977, ProviderMeta: meta("80", "")},
			{Path: "spotify:track:2", Title: "Hold Me", Artist: "Fleetwood Mac", Year: 1982, ProviderMeta: meta("60", "true")},
			{Path: "spotify:track:3", Title: "The Chain", Artist: "Fleetwood Mac", Year: 1977, ProviderMeta: meta("70", "")},
		},
		Discography: []provider.AlbumInfo{
			{ID: "al1", Name: "Rumours", Artist: "Fleetwood Mac", Year: 1977, TrackCount: 11},
			{ID: "al2", Name: "Mirage", Artist: "Fleetwood Mac", Year: 1982, TrackCount: 12},
		},
	}
}

func newArtistTestModel(fake *fakeSpotifyProvider) Model {
	m := newSpotifyTestModel(fake)
	m.plVisible = 12
	return m
}

// TestArtistScreenOpenFromSearchArtistTab pins the search drill replacement:
// enter on the Artists tab must open the artist screen, not the album
// fallback drill.
func TestArtistScreenOpenFromSearchArtistTab(t *testing.T) {
	fake := &fakeSpotifyProvider{name: "Spotify", searchAll: spotSearchAllFixture()}
	fake.artistDetails = map[string]provider.ArtistDetail{"ar1": artistDetailFixture()}
	m := newArtistTestModel(fake)
	m = openSpotMultiSearch(t, m)

	// Tracks → Albums → Artists.
	for range 2 {
		updated, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyRight})
		m = updated.(Model)
	}
	if m.spotSearch.tab != spotTabArtists {
		t.Fatalf("tab = %d, want artists", m.spotSearch.tab)
	}

	updated, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = updated.(Model)
	msg, ok := cmd().(artistDetailMsg)
	if !ok {
		t.Fatalf("enter on artist produced %T; want artistDetailMsg", cmd())
	}
	if !m.artist.visible || m.artist.loading != true {
		t.Fatalf("artist state = visible:%v loading:%v; want open and loading", m.artist.visible, m.artist.loading)
	}
	if len(m.spotSearch.drill) != 0 {
		t.Fatalf("spot drill = %+v; want no album fallback level", m.spotSearch.drill)
	}
	if m.activeScreen() != screenArtist {
		t.Fatalf("activeScreen = %v, want screenArtist", m.activeScreen())
	}

	updated, _ = m.Update(msg)
	m = updated.(Model)
	if m.artist.loading || len(m.artist.detail.Popular) != 3 {
		t.Fatalf("artist loading = %v popular = %d; want detail applied", m.artist.loading, len(m.artist.detail.Popular))
	}
	if !m.spotSearch.visible || m.spotSearch.tab != spotTabArtists || m.spotSearch.cursor != 0 {
		t.Fatalf("search overlay = visible:%v tab:%d cursor:%d; want mounted underneath with cursor intact",
			m.spotSearch.visible, m.spotSearch.tab, m.spotSearch.cursor)
	}
}

// TestArtistScreenOpenFromNavBrowserArtist enters an artist row in the nav
// browser and expects the artist screen on top with the browser still mounted.
func TestArtistScreenOpenFromNavBrowserArtist(t *testing.T) {
	fake := &fakeSpotifyProvider{name: "Spotify", artistsList: []provider.ArtistInfo{artistFixtureArtist()}}
	fake.artistDetails = map[string]provider.ArtistDetail{"ar1": artistDetailFixture()}
	m := newArtistTestModel(fake)
	m.navBrowser = navBrowserState{
		prov:    fake,
		visible: true,
		mode:    navBrowseModeByArtist,
		screen:  navBrowseScreenList,
		artists: fake.artistsList,
	}

	updated, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = updated.(Model)
	msg, ok := cmd().(artistDetailMsg)
	if !ok {
		t.Fatalf("enter on nav artist produced %T; want artistDetailMsg", cmd())
	}
	if !m.artist.visible || !m.navBrowser.visible {
		t.Fatalf("artist = %v nav = %v; want artist over mounted nav browser", m.artist.visible, m.navBrowser.visible)
	}

	updated, _ = m.Update(msg)
	m = updated.(Model)

	// Esc pops the artist screen back to the artist list with cursor intact.
	updated, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	m = updated.(Model)
	if m.artist.visible {
		t.Fatal("esc should close the artist screen")
	}
	if !m.navBrowser.visible || m.navBrowser.cursor != 0 || m.navBrowser.screen != navBrowseScreenList {
		t.Fatalf("nav = visible:%v cursor:%d screen:%d; want restored list", m.navBrowser.visible, m.navBrowser.cursor, m.navBrowser.screen)
	}
}

// TestArtistScreenLoadingState verifies the loading body renders while the
// detail fetch is in flight.
func TestArtistScreenLoadingState(t *testing.T) {
	fake := &fakeSpotifyProvider{name: "Spotify"}
	fake.artistDetails = map[string]provider.ArtistDetail{"ar1": artistDetailFixture()}
	m := newArtistTestModel(fake)
	if m.openArtistScreen("Spotify", artistFixtureArtist()) == nil {
		t.Fatal("openArtistScreen returned nil command")
	}
	if !m.artist.loading {
		t.Fatal("artist screen should be loading right after open")
	}
	body := m.renderArtistBody()
	if !strings.Contains(body, "Loading artist") {
		t.Fatalf("loading body %q missing indicator", body)
	}
}

// TestArtistScreenStaleGenDropped verifies a superseded open drops the first
// completion (generation guard).
func TestArtistScreenStaleGenDropped(t *testing.T) {
	fake := &fakeSpotifyProvider{name: "Spotify"}
	fake.artistDetails = map[string]provider.ArtistDetail{
		"ar1": artistDetailFixture(),
		"ar2": {Info: provider.ArtistInfo{ID: "ar2", Name: "ABBA"}},
	}
	m := newArtistTestModel(fake)
	stale := m.openArtistScreen("Spotify", artistFixtureArtist())
	current := m.openArtistScreen("Spotify", provider.ArtistInfo{ID: "ar2", Name: "ABBA"})

	updated, _ := m.Update(stale())
	m = updated.(Model)
	if !m.artist.loading || m.artist.info.ID != "ar2" {
		t.Fatalf("stale completion applied: loading = %v id = %s", m.artist.loading, m.artist.info.ID)
	}
	updated, _ = m.Update(current())
	m = updated.(Model)
	if m.artist.loading || m.artist.info.ID != "ar2" {
		t.Fatalf("current completion dropped: loading = %v id = %s", m.artist.loading, m.artist.info.ID)
	}
}

// TestArtistScreenWrongProviderDropped verifies the providerName guard.
func TestArtistScreenWrongProviderDropped(t *testing.T) {
	fake := &fakeSpotifyProvider{name: "Spotify"}
	fake.artistDetails = map[string]provider.ArtistDetail{"ar1": artistDetailFixture()}
	m := newArtistTestModel(fake)
	m.openArtistScreen("Spotify", artistFixtureArtist())

	msg := artistDetailMsg{artistID: "ar1", detail: artistDetailFixture(), providerName: "Other", gen: m.requests.artist}
	updated, _ := m.Update(msg)
	m = updated.(Model)
	if !m.artist.loading || len(m.artist.detail.Popular) != 0 {
		t.Fatal("wrong-provider completion should be dropped")
	}
}

// TestArtistScreenErrorPopsWithToast verifies the failed-drill convention:
// status toast plus pop back to the opening surface.
func TestArtistScreenErrorPopsWithToast(t *testing.T) {
	fake := &fakeSpotifyProvider{name: "Spotify", searchAll: spotSearchAllFixture()}
	// No entry for "ar1": the detail fetch fails without breaking the search.
	fake.artistDetails = map[string]provider.ArtistDetail{}
	m := newArtistTestModel(fake)
	m = openSpotMultiSearch(t, m)
	for range 2 {
		updated, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyRight})
		m = updated.(Model)
	}
	updated, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = updated.(Model)
	msg := cmd().(artistDetailMsg)

	updated, _ = m.Update(msg)
	m = updated.(Model)
	if m.artist.visible {
		t.Fatal("failed load should pop the artist screen")
	}
	if !strings.Contains(m.status.text, "Artist load failed") {
		t.Fatalf("status = %q, want load-failed toast", m.status.text)
	}
	if !m.spotSearch.visible || m.spotSearch.tab != spotTabArtists {
		t.Fatal("failed load should return to the search results")
	}
}

// TestArtistScreenSectionsInOrder verifies the rendered layout: profile info
// line, then Popular / Liked Songs / Discography headers with their rows.
func TestArtistScreenSectionsInOrder(t *testing.T) {
	fake := &fakeSpotifyProvider{name: "Spotify"}
	fake.artistDetails = map[string]provider.ArtistDetail{"ar1": artistDetailFixture()}
	m := newArtistTestModel(fake)
	m.openArtistScreen("Spotify", artistFixtureArtist())
	msg := artistDetailMsg{artistID: "ar1", detail: artistDetailFixture(), providerName: "Spotify", gen: m.requests.artist}
	updated, _ := m.Update(msg)
	m = updated.(Model)

	oldPanelWidth := ui.PanelWidth
	ui.PanelWidth = 60
	t.Cleanup(func() { ui.PanelWidth = oldPanelWidth })

	body := m.renderArtistBody()
	for _, want := range []string{
		"1234567 followers",
		"rock, classic rock",
		"Popular (by popularity)",
		"Liked Songs",
		"Discography",
		"Dreams",
		"Hold Me",
		"The Chain",
		"Rumours — Fleetwood Mac (1977)",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("body %q missing %q", body, want)
		}
	}
	order := []string{"Popular", "Liked Songs", "Discography"}
	last := -1
	for _, label := range order {
		idx := strings.Index(body, label)
		if idx < 0 || idx < last {
			t.Fatalf("sections out of order in %q", body)
		}
		last = idx
	}
}

// TestArtistScreenSortCycles walks all three sort modes with a fixture whose
// popularity, year, and liked marks each produce a distinct order.
func TestArtistScreenSortCycles(t *testing.T) {
	detail := provider.ArtistDetail{
		Info: artistFixtureArtist(),
		Popular: []playlist.Track{
			{Path: "spotify:track:a", Title: "Alpha", Artist: "Fleetwood Mac", Year: 1977,
				ProviderMeta: map[string]string{provider.MetaSpotifyPopularity: "80"}},
			{Path: "spotify:track:b", Title: "Bravo", Artist: "Fleetwood Mac", Year: 1982,
				ProviderMeta: map[string]string{provider.MetaSpotifyPopularity: "60", provider.MetaSpotifyLiked: "true"}},
			{Path: "spotify:track:c", Title: "Charlie", Artist: "Fleetwood Mac", Year: 1975,
				ProviderMeta: map[string]string{provider.MetaSpotifyPopularity: "70", provider.MetaSpotifyLiked: "true"}},
		},
	}
	fake := &fakeSpotifyProvider{name: "Spotify"}
	fake.artistDetails = map[string]provider.ArtistDetail{"ar1": detail}
	m := newArtistTestModel(fake)
	m.openArtistScreen("Spotify", artistFixtureArtist())
	msg := artistDetailMsg{artistID: "ar1", detail: detail, providerName: "Spotify", gen: m.requests.artist}
	updated, _ := m.Update(msg)
	m = updated.(Model)

	titles := func() []string {
		popular := m.artistPopular()
		out := make([]string, len(popular))
		for i, tr := range popular {
			out[i] = tr.Title
		}
		return out
	}
	eq := func(want ...string) bool {
		got := titles()
		if len(got) != len(want) {
			return false
		}
		for i := range want {
			if got[i] != want[i] {
				return false
			}
		}
		return true
	}

	if !eq("Alpha", "Charlie", "Bravo") {
		t.Fatalf("default order = %v, want popularity desc", titles())
	}
	for _, step := range []struct {
		key  tea.KeyPressMsg
		want []string
		note string
	}{
		{tea.KeyPressMsg{Text: "s"}, []string{"Bravo", "Alpha", "Charlie"}, "recency"},
		{tea.KeyPressMsg{Text: "s"}, []string{"Charlie", "Bravo", "Alpha"}, "liked first (stable by popularity)"},
		{tea.KeyPressMsg{Text: "s"}, []string{"Alpha", "Charlie", "Bravo"}, "back to popularity"},
	} {
		updated, _ := m.Update(step.key)
		m = updated.(Model)
		if !eq(step.want...) {
			t.Fatalf("%s: order = %v, want %v", step.note, titles(), step.want)
		}
	}
	if m.artist.sort != artistSortPopularity {
		t.Fatalf("sort = %d, want wrap-around to popularity", m.artist.sort)
	}
}

// TestArtistScreenEnterPopularPlaysAndQueues pins the play-row-and-enqueue-
// remainder behavior: enter on the first Popular row plays it and enqueues
// the rest of the section.
func TestArtistScreenEnterPopularPlaysAndQueues(t *testing.T) {
	fake := &fakeSpotifyProvider{name: "Spotify"}
	fake.artistDetails = map[string]provider.ArtistDetail{"ar1": artistDetailFixture()}
	m := newArtistTestModel(fake)
	m.openArtistScreen("Spotify", artistFixtureArtist())
	msg := artistDetailMsg{artistID: "ar1", detail: artistDetailFixture(), providerName: "Spotify", gen: m.requests.artist}
	updated, _ := m.Update(msg)
	m = updated.(Model)

	updated, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = updated.(Model)
	if got := m.playlist.Len(); got != 3 {
		t.Fatalf("queue len = %d, want the whole popular section", got)
	}
	paths := trackPathsOf(m.playlist.Tracks())
	if paths[0] != "spotify:track:1" {
		t.Fatalf("first queued = %v, want Dreams first", paths)
	}
	if m.playlist.Index() != 0 {
		t.Fatalf("index = %d, want playback started at the played row", m.playlist.Index())
	}
	if !strings.Contains(m.status.text, "+2 queued") {
		t.Fatalf("status = %q, want remainder toast", m.status.text)
	}
	if !m.artist.visible {
		t.Fatal("artist screen should stay open after playing")
	}
}

// TestArtistScreenEnterLikedQueuesLikedSection verifies enter on a Liked row
// enqueues only the liked section's remainder.
func TestArtistScreenEnterLikedQueuesLikedSection(t *testing.T) {
	fake := &fakeSpotifyProvider{name: "Spotify"}
	fake.artistDetails = map[string]provider.ArtistDetail{"ar1": artistDetailFixture()}
	m := newArtistTestModel(fake)
	m.openArtistScreen("Spotify", artistFixtureArtist())
	msg := artistDetailMsg{artistID: "ar1", detail: artistDetailFixture(), providerName: "Spotify", gen: m.requests.artist}
	updated, _ := m.Update(msg)
	m = updated.(Model)

	// Row layout: 3 popular, then liked (row 3), then discography.
	for range 3 {
		updated, _ := m.Update(tea.KeyPressMsg{Text: "j"})
		m = updated.(Model)
	}
	updated, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = updated.(Model)
	if got := m.playlist.Len(); got != 1 {
		t.Fatalf("queue len = %d, want only the liked section", got)
	}
	if m.playlist.Tracks()[0].Title != "Hold Me" {
		t.Fatalf("queued = %v, want Hold Me", m.playlist.Tracks())
	}
}

// TestArtistScreenDiscographyDrillAndBack covers enter on a Discography row
// (pushes an album drill) and esc returning to the artist screen, then the
// opening surface.
func TestArtistScreenDiscographyDrillAndBack(t *testing.T) {
	fake := &fakeSpotifyProvider{name: "Spotify", searchAll: spotSearchAllFixture()}
	fake.artistDetails = map[string]provider.ArtistDetail{"ar1": artistDetailFixture()}
	fake.albumTracks = map[string][]playlist.Track{
		"al1": {{Path: "spotify:track:9", Title: "Second Hand News", Artist: "Fleetwood Mac"}},
	}
	m := newArtistTestModel(fake)
	m = openSpotMultiSearch(t, m)
	for range 2 {
		updated, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyRight})
		m = updated.(Model)
	}
	updated, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = updated.(Model)
	updated, _ = m.Update(cmd().(artistDetailMsg))
	m = updated.(Model)

	// Move to the first Discography row (3 popular + 1 liked + 1 = row 4).
	for range 4 {
		updated, _ := m.Update(tea.KeyPressMsg{Text: "j"})
		m = updated.(Model)
	}
	if row, ok := m.artistRowAt(); !ok || row.kind != artistRowDiscography || row.album.ID != "al1" {
		t.Fatalf("row at cursor = %+v ok=%v, want Rumours discography row", row, ok)
	}

	updated, cmd = m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = updated.(Model)
	albumMsg, ok := cmd().(artistAlbumTracksMsg)
	if !ok {
		t.Fatalf("enter on discography produced %T; want artistAlbumTracksMsg", cmd())
	}
	if len(m.artist.drill) != 1 || m.artist.drill[0].crumb != "Album — Rumours" {
		t.Fatalf("drill = %+v, want Album — Rumours level", m.artist.drill)
	}
	updated, _ = m.Update(albumMsg)
	m = updated.(Model)
	if tracks := m.artist.drill[0].tracks; len(tracks) != 1 || tracks[0].Title != "Second Hand News" {
		t.Fatalf("drill tracks = %+v", m.artist.drill[0].tracks)
	}

	oldPanelWidth := ui.PanelWidth
	ui.PanelWidth = 60
	t.Cleanup(func() { ui.PanelWidth = oldPanelWidth })
	if body := m.renderArtistBody(); !strings.Contains(body, "Album — Rumours") {
		t.Fatalf("drill body %q missing crumb", body)
	}

	// First esc pops the album drill back to the artist sections.
	updated, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	m = updated.(Model)
	if len(m.artist.drill) != 0 || !m.artist.visible {
		t.Fatalf("drill = %+v visible = %v; want artist sections back", m.artist.drill, m.artist.visible)
	}
	if row, ok := m.artistRowAt(); !ok || row.kind != artistRowDiscography {
		t.Fatalf("cursor row = %+v ok=%v, want discography cursor preserved", row, ok)
	}

	// Second esc pops the artist screen back to the search artist tab.
	updated, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	m = updated.(Model)
	if m.artist.visible || !m.spotSearch.visible || m.spotSearch.tab != spotTabArtists {
		t.Fatalf("artist = %v search = %v tab = %d; want popped to search results", m.artist.visible, m.spotSearch.visible, m.spotSearch.tab)
	}
}

// TestArtistScreenFollowToggle pins the f key against the fake ArtistFollower
// using the session-local toggle state.
func TestArtistScreenFollowToggle(t *testing.T) {
	fake := &fakeSpotifyProvider{name: "Spotify"}
	fake.artistDetails = map[string]provider.ArtistDetail{"ar1": artistDetailFixture()}
	m := newArtistTestModel(fake)
	m.openArtistScreen("Spotify", artistFixtureArtist())
	msg := artistDetailMsg{artistID: "ar1", detail: artistDetailFixture(), providerName: "Spotify", gen: m.requests.artist}
	updated, _ := m.Update(msg)
	m = updated.(Model)

	updated, cmd := m.Update(tea.KeyPressMsg{Text: "f"})
	m = updated.(Model)
	followMsg, ok := cmd().(artistFollowedMsg)
	if !ok || !followMsg.follow {
		t.Fatalf("f produced %T %+v; want follow", cmd(), cmd())
	}
	updated, _ = m.Update(followMsg)
	m = updated.(Model)
	if len(fake.followedArtists) != 1 || fake.followedArtists[0] != "ar1" {
		t.Fatalf("followedArtists = %v", fake.followedArtists)
	}
	if !strings.Contains(m.status.text, "Following Fleetwood Mac") {
		t.Fatalf("status = %q", m.status.text)
	}

	updated, cmd = m.Update(tea.KeyPressMsg{Text: "f"})
	m = updated.(Model)
	unfollowMsg, ok := cmd().(artistFollowedMsg)
	if !ok || unfollowMsg.follow {
		t.Fatalf("second f produced %T %+v; want unfollow", cmd(), cmd())
	}
	updated, _ = m.Update(unfollowMsg)
	m = updated.(Model)
	if len(fake.unfollowedArtists) != 1 || fake.unfollowedArtists[0] != "ar1" {
		t.Fatalf("unfollowedArtists = %v", fake.unfollowedArtists)
	}
}

// TestArtistScreenRowActionsMirrorSearchDrill exercises one representative
// row action (q = queue next) dispatching to the same handler the search
// drill lists use.
func TestArtistScreenRowActionsMirrorSearchDrill(t *testing.T) {
	fake := &fakeSpotifyProvider{name: "Spotify"}
	fake.artistDetails = map[string]provider.ArtistDetail{"ar1": artistDetailFixture()}
	m := newArtistTestModel(fake)
	m.openArtistScreen("Spotify", artistFixtureArtist())
	msg := artistDetailMsg{artistID: "ar1", detail: artistDetailFixture(), providerName: "Spotify", gen: m.requests.artist}
	updated, _ := m.Update(msg)
	m = updated.(Model)

	updated, _ = m.Update(tea.KeyPressMsg{Text: "q"})
	m = updated.(Model)
	if m.playlist.Len() != 1 || m.playlist.Tracks()[0].Title != "Dreams" {
		t.Fatalf("queue = %+v, want first popular row queued next", m.playlist.Tracks())
	}
	if !m.artist.visible {
		t.Fatal("q should keep the artist screen open")
	}
	if !strings.Contains(m.status.text, "Queued:") {
		t.Fatalf("status = %q, want queue toast", m.status.text)
	}
}

// TestRegistryCoversArtistKeys keeps the new screen's reserved keys in the
// command registry so plugins cannot shadow them.
func TestRegistryCoversArtistKeys(t *testing.T) {
	for _, key := range []string{"s", "f", "*"} {
		found := false
		for _, c := range commandRegistry {
			if c.Mode&commandModeArtist == 0 {
				continue
			}
			for _, k := range c.Keys {
				if k == key {
					found = true
				}
			}
		}
		if !found {
			t.Errorf("commandRegistry has no %q entry for commandModeArtist", key)
		}
	}
}
