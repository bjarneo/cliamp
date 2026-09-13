package model

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/bjarneo/cliamp/playlist"
	"github.com/bjarneo/cliamp/provider"
	"github.com/bjarneo/cliamp/ui"
)

// home_overlay_test.go covers the Home view against the frozen provider
// contracts, using the shared fakeSpotifyProvider plumbing. The homeFake
// wrapper adds the album-browsing interfaces (AlbumBrowser, AlbumSortSaver)
// without putting the base fake on those contracts.

type homeFakeProvider struct {
	*fakeSpotifyProvider
	albumSorts []provider.SortType
	albumPages map[string][]provider.AlbumInfo
	savedSorts []string
}

func (f *homeFakeProvider) AlbumList(sortType string, offset, size int) ([]provider.AlbumInfo, error) {
	albums := f.albumPages[sortType]
	if offset >= len(albums) {
		return nil, nil
	}
	end := min(offset+size, len(albums))
	return albums[offset:end], nil
}

func (f *homeFakeProvider) AlbumSortTypes() []provider.SortType { return f.albumSorts }

func (f *homeFakeProvider) DefaultAlbumSort() string { return f.albumSorts[0].ID }

func (f *homeFakeProvider) SaveAlbumSort(sortType string) error {
	f.savedSorts = append(f.savedSorts, sortType)
	return nil
}

func homeFixture() *homeFakeProvider {
	return &homeFakeProvider{
		fakeSpotifyProvider: &fakeSpotifyProvider{
			name: "Spotify",
			lists: []playlist.PlaylistInfo{
				{ID: "pl1", Name: "Road Trips", TrackCount: 5},
				{ID: "pl2", Name: "Focus", TrackCount: 3},
			},
			artistsList: []provider.ArtistInfo{
				{ID: "ar1", Name: "Fleetwood Mac", AlbumCount: 2},
				{ID: "ar2", Name: "ABBA", AlbumCount: 9},
			},
			albumTracks: map[string][]playlist.Track{
				"al1": {
					{Path: "spotify:track:10", Title: "Second Hand News", Artist: "Fleetwood Mac"},
					{Path: "spotify:track:11", Title: "Dreams", Artist: "Fleetwood Mac"},
				},
			},
		},
		albumSorts: []provider.SortType{
			{ID: "recent", Label: "Recently Added"},
			{ID: "alpha", Label: "By Name"},
		},
		albumPages: map[string][]provider.AlbumInfo{
			"recent": {
				{ID: "al1", Name: "Rumours", Artist: "Fleetwood Mac", Year: 1977},
				{ID: "al2", Name: "Mirage", Artist: "Fleetwood Mac", Year: 1982},
				{ID: "al3", Name: "Tusk", Artist: "Fleetwood Mac", Year: 1979},
			},
			"alpha": {
				{ID: "al2", Name: "Mirage", Artist: "Fleetwood Mac", Year: 1982},
				{ID: "al1", Name: "Rumours", Artist: "Fleetwood Mac", Year: 1977},
				{ID: "al3", Name: "Tusk", Artist: "Fleetwood Mac", Year: 1979},
			},
		},
	}
}

func newHomeTestModel(p playlist.Provider) Model {
	player := &playbackFakeEngine{}
	return Model{
		player:        player,
		playlist:      playlist.New(),
		localProvider: commandsTestProvider{name: "Local"},
		provider:      p,
		providers:     []ProviderEntry{{Key: "spotify", Name: "Spotify", Provider: p}},
		vis:           ui.NewVisualizer(float64(player.SampleRate())),
	}
}

// applyHomeCmd runs a Home command chain to completion, applying every
// message (batches and page chains included) to the model.
func applyHomeCmd(t *testing.T, m Model, cmd tea.Cmd) Model {
	t.Helper()
	for cmd != nil {
		msg := cmd()
		if batch, ok := msg.(tea.BatchMsg); ok {
			for _, sub := range batch {
				m = applyHomeCmd(t, m, sub)
			}
			return m
		}
		updated, next := m.Update(msg)
		m = updated.(Model)
		cmd = next
	}
	return m
}

func openHome(t *testing.T, m Model) Model {
	t.Helper()
	cmd := m.openHomeView()
	if cmd == nil {
		t.Fatal("openHomeView returned nil command")
	}
	return applyHomeCmd(t, m, cmd)
}

func pressHomeKey(t *testing.T, m Model, key tea.KeyPressMsg) Model {
	t.Helper()
	updated, cmd := m.Update(key)
	return applyHomeCmd(t, updated.(Model), cmd)
}

func homeAlbumNames(m Model) []string {
	albums := m.homeAlbumsView()
	out := make([]string, len(albums))
	for i, a := range albums {
		out[i] = a.Name
	}
	return out
}

// TestHomeToggleOpenClose pins the H binding in the main view and the close
// path from inside Home.
func TestHomeToggleOpenClose(t *testing.T) {
	m := newHomeTestModel(homeFixture())
	updated, cmd := m.Update(tea.KeyPressMsg{Text: "H"})
	m = applyHomeCmd(t, updated.(Model), cmd)
	if !m.home.visible || m.activeScreen() != screenHome {
		t.Fatalf("home = visible:%v screen:%v; want open", m.home.visible, m.activeScreen())
	}
	if !m.usesContentFirstLayout() {
		t.Fatal("home should use the content-first layout")
	}

	// H inside Home closes it.
	m = pressHomeKey(t, m, tea.KeyPressMsg{Text: "H"})
	if m.home.visible || m.activeScreen() != screenMain {
		t.Fatalf("home = visible:%v screen:%v; want closed", m.home.visible, m.activeScreen())
	}
}

// TestHomeStaleGenDropped verifies closing Home supersedes in-flight fetches.
func TestHomeStaleGenDropped(t *testing.T) {
	m := newHomeTestModel(homeFixture())
	cmd := m.openHomeView()
	m.closeHomeView()
	m = applyHomeCmd(t, m, cmd)
	if m.home.visible || m.home.lists != nil {
		t.Fatalf("home = visible:%v lists:%v; want dropped after close", m.home.visible, m.home.lists)
	}
}

// TestHomeSectionsRender verifies the two-pane body: all three sections with
// counts, rows, the focusable sidebar header, and the content hint.
func TestHomeSectionsRender(t *testing.T) {
	m := openHome(t, newHomeTestModel(homeFixture()))
	m.plVisible = 12

	oldPanelWidth := ui.PanelWidth
	ui.PanelWidth = 100
	t.Cleanup(func() { ui.PanelWidth = oldPanelWidth })

	body := m.renderHomeBody()
	for _, want := range []string{
		"Library · recents",
		"Playlists (2)",
		"Albums (3)",
		"Artists (2)",
		"+ New playlist",
		"Road Trips",
		"Rumours",
		"Fleetwood Mac",
		"Select a playlist, album, or artist",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("body missing %q", want)
		}
	}
	if got := strings.Count(body, "\n") + 1; got > 12 {
		t.Fatalf("body height = %d rows; want <= 12 (the playlist-region budget)", got)
	}
	// Two panes: the album row and the content hint must share lines.
	for _, line := range strings.Split(body, "\n") {
		if strings.Contains(line, "Rumours") && strings.Contains(line, "Select a playlist") {
			t.Fatalf("panes rendered on separate rows: %q", line)
		}
	}
}

// TestHomeSectionsOmittedWithoutInterface verifies honest degradation: the
// Albums section requires AlbumBrowser, Artists requires ArtistBrowser, and
// Playlists (base contract) always renders.
func TestHomeSectionsOmittedWithoutInterface(t *testing.T) {
	cases := []struct {
		name    string
		prov    playlist.Provider
		want    []string
		notWant []string
	}{
		{
			name: "playlists only",
			prov: commandsTestProvider{name: "Local", lists: []playlist.PlaylistInfo{{ID: "pl1", Name: "Mix"}}},
			want: []string{"Playlists (1)", "Mix"},
		},
		{
			name: "no album browser",
			prov: &fakeSpotifyProvider{
				name:        "Spotify",
				lists:       []playlist.PlaylistInfo{{ID: "pl1", Name: "Road Trips"}},
				artistsList: []provider.ArtistInfo{{ID: "ar1", Name: "Fleetwood Mac"}},
			},
			want: []string{"Playlists (1)", "Artists (1)"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := openHome(t, newHomeTestModel(tc.prov))
			m.plVisible = 12
			oldPanelWidth := ui.PanelWidth
			ui.PanelWidth = 100
			t.Cleanup(func() { ui.PanelWidth = oldPanelWidth })

			body := m.renderHomeBody()
			for _, want := range tc.want {
				if !strings.Contains(body, want) {
					t.Errorf("body missing %q", want)
				}
			}
			for _, notWant := range []string{"Albums (", "Albums (loading", "Albums (none)"} {
				if strings.Contains(body, notWant) {
					t.Errorf("body should omit albums section, found %q", notWant)
				}
			}
		})
	}
}

// TestHomeFilterFiltersAllSections types a `/` filter and verifies every
// section narrows client-side while its header stays visible.
func TestHomeFilterFiltersAllSections(t *testing.T) {
	m := openHome(t, newHomeTestModel(homeFixture()))
	m = pressHomeKey(t, m, tea.KeyPressMsg{Text: "/"})
	for _, ch := range "road" {
		m = pressHomeKey(t, m, tea.KeyPressMsg{Text: string(ch)})
	}
	m = pressHomeKey(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})

	if m.home.filter != "road" || m.home.filtering {
		t.Fatalf("filter = %q filtering = %v; want committed filter", m.home.filter, m.home.filtering)
	}
	var names []string
	for _, row := range m.homeRows() {
		if row.kind != homeRowNew {
			names = append(names, homeRowLabel(row))
		}
	}
	if len(names) != 1 || names[0] != "Road Trips · 5 tracks" {
		t.Fatalf("filtered rows = %v; want only Road Trips", names)
	}

	m.plVisible = 12
	oldPanelWidth := ui.PanelWidth
	ui.PanelWidth = 100
	t.Cleanup(func() { ui.PanelWidth = oldPanelWidth })
	body := m.renderHomeBody()
	for _, want := range []string{"Playlists (1/2)", "Albums (0/3)", "Artists (0/2)"} {
		if !strings.Contains(body, want) {
			t.Errorf("body missing section header %q", want)
		}
	}
}

// TestHomeOrderCycles walks all three sidebar orderings; the album fixture
// gives each mode a distinct order.
func TestHomeOrderCycles(t *testing.T) {
	m := openHome(t, newHomeTestModel(homeFixture()))
	if got := homeAlbumNames(m); strings.Join(got, ",") != "Rumours,Mirage,Tusk" {
		t.Fatalf("recents order = %v", got)
	}
	steps := []struct {
		want []string
		note string
	}{
		{[]string{"Tusk", "Mirage", "Rumours"}, "recently added reverses fetch order"},
		{[]string{"Mirage", "Rumours", "Tusk"}, "alphabetical"},
		{[]string{"Rumours", "Mirage", "Tusk"}, "back to recents"},
	}
	for _, step := range steps {
		m = pressHomeKey(t, m, tea.KeyPressMsg{Text: "s"})
		if got := homeAlbumNames(m); strings.Join(got, ",") != strings.Join(step.want, ",") {
			t.Fatalf("%s: order = %v, want %v", step.note, got, step.want)
		}
	}
	// Playlists follow the same cycle (alphabetical differs, added = provider order).
	m2 := openHome(t, newHomeTestModel(homeFixture()))
	m2 = pressHomeKey(t, m2, tea.KeyPressMsg{Text: "s"}) // recently added
	if got := m2.homePlaylistsView(); got[0].Name != "Road Trips" {
		t.Fatalf("playlists recently-added order = %v; want provider order", got)
	}
	m2 = pressHomeKey(t, m2, tea.KeyPressMsg{Text: "s"}) // alphabetical
	if view := m2.homePlaylistsView(); view[0].Name != "Focus" || view[1].Name != "Road Trips" {
		t.Fatalf("playlists alphabetical = %v", view)
	}
}

// TestHomeAlbumSortCyclePersists pins the S key: provider sort advances,
// SaveAlbumSort is called, and the list refetches under the new sort.
func TestHomeAlbumSortCyclePersists(t *testing.T) {
	fake := homeFixture()
	m := openHome(t, newHomeTestModel(fake))

	m = pressHomeKey(t, m, tea.KeyPressMsg{Text: "S"})
	if m.home.sortType != "alpha" {
		t.Fatalf("sortType = %q; want alpha", m.home.sortType)
	}
	if len(fake.savedSorts) != 1 || fake.savedSorts[0] != "alpha" {
		t.Fatalf("savedSorts = %v; want persisted alpha", fake.savedSorts)
	}
	if got := homeAlbumNames(m); strings.Join(got, ",") != "Mirage,Rumours,Tusk" {
		t.Fatalf("albums after sort cycle = %v", got)
	}
	if !strings.Contains(m.status.text, "By Name") {
		t.Fatalf("status = %q; want sort label toast", m.status.text)
	}
}

// TestHomeAlbumsLazyPageOnScroll verifies the sidebar lazy-loads the next
// AlbumList page when the cursor reaches the end of the loaded section.
func TestHomeAlbumsLazyPageOnScroll(t *testing.T) {
	fake := homeFixture()
	fake.artistsList = nil
	albums := make([]provider.AlbumInfo, 105)
	for i := range albums {
		albums[i] = provider.AlbumInfo{ID: "al", Name: "Album", Artist: "X"}
	}
	fake.albumPages["recent"] = albums
	fake.albumPages["alpha"] = albums
	m := openHome(t, newHomeTestModel(fake))
	if len(m.home.albums) != 100 {
		t.Fatalf("first page = %d albums; want navAlbumPageSize", len(m.home.albums))
	}

	m = pressHomeKey(t, m, tea.KeyPressMsg{Code: tea.KeyEnd})
	if len(m.home.albums) != 105 {
		t.Fatalf("albums after end-jump = %d; want the second page appended", len(m.home.albums))
	}
	if !m.home.albumsDone {
		t.Fatal("albums should be done after the short second page")
	}
}

// TestHomeFocusSwitching covers Tab and ctrl+arrow pane cycling: content
// focus is only reachable once something is open.
func TestHomeFocusSwitching(t *testing.T) {
	fake := homeFixture()
	fake.allTracks = spotifyTracks(3)
	m := openHome(t, newHomeTestModel(fake))

	m = pressHomeKey(t, m, tea.KeyPressMsg{Code: tea.KeyTab})
	if m.home.focus != homePaneSidebar {
		t.Fatal("tab with nothing open should keep sidebar focus")
	}

	// Move to the Road Trips row and open it.
	m = pressHomeKey(t, m, tea.KeyPressMsg{Text: "j"})
	m = pressHomeKey(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
	if m.home.focus != homePaneContent || m.home.content.kind != homeContentPlaylist {
		t.Fatalf("focus = %d kind = %d; want content pane on playlist", m.home.focus, m.home.content.kind)
	}
	m = pressHomeKey(t, m, tea.KeyPressMsg{Code: tea.KeyTab})
	if m.home.focus != homePaneSidebar {
		t.Fatal("tab should return to sidebar")
	}
	m = pressHomeKey(t, m, tea.KeyPressMsg{Code: tea.KeyRight, Mod: tea.ModCtrl})
	if m.home.focus != homePaneContent {
		t.Fatal("ctrl+right should focus the content pane")
	}
}

// TestHomePlaylistEnterPagesIncrementally drives a paginated playlist load
// through the fake TrackPager and verifies the queue stays untouched.
func TestHomePlaylistEnterPagesIncrementally(t *testing.T) {
	fake := homeFixture()
	fake.allTracks = spotifyTracks(5)
	fake.pageLimit = 2
	m := openHome(t, newHomeTestModel(fake))

	m = pressHomeKey(t, m, tea.KeyPressMsg{Text: "j"}) // Road Trips
	m = pressHomeKey(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})

	if got := len(m.home.content.tracks); got != 5 {
		t.Fatalf("content tracks = %d; want all 5 pages applied", got)
	}
	want := []string{"pl1:0:200", "pl1:2:200", "pl1:4:200"}
	if len(fake.pageCalls) != len(want) {
		t.Fatalf("pageCalls = %v; want %v", fake.pageCalls, want)
	}
	for i := range want {
		if fake.pageCalls[i] != want[i] {
			t.Fatalf("pageCalls = %v; want %v", fake.pageCalls, want)
		}
	}
	if m.playlist.Len() != 0 {
		t.Fatalf("queue len = %d; want browsing not to touch the queue", m.playlist.Len())
	}
	if m.home.content.paging.active {
		t.Fatal("paging should be finished")
	}
	if crumb := m.homeContentCrumb(); crumb != "Home / Playlists / Road Trips" {
		t.Fatalf("crumb = %q", crumb)
	}
}

// TestHomePlaylistEnterFallsBackWhenQueuePaging pins the queue-conflict
// guard: when the queue is incrementally loading the same playlist, the
// content pane loads it with a one-shot Tracks() instead of TracksPage.
func TestHomePlaylistEnterFallsBackWhenQueuePaging(t *testing.T) {
	fake := homeFixture()
	fake.allTracks = spotifyTracks(5)
	m := openHome(t, newHomeTestModel(fake))
	m.trackPaging = trackPagingState{active: true, playlistID: "pl1", offset: 2, total: 5}

	m = pressHomeKey(t, m, tea.KeyPressMsg{Text: "j"})
	m = pressHomeKey(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})

	if len(fake.pageCalls) != 0 {
		t.Fatalf("pageCalls = %v; want no pager reads while the queue pages this playlist", fake.pageCalls)
	}
	if got := len(m.home.content.tracks); got != 5 {
		t.Fatalf("content tracks = %d; want the one-shot Tracks() load", got)
	}
	if m.home.content.paging.active {
		t.Fatal("fallback load must not arm paging")
	}
}

// TestHomeQueueLoadCancelsContentPaging pins the reverse guard: the queue
// loading a playlist that Home is paging cancels Home's page chain first.
func TestHomeQueueLoadCancelsContentPaging(t *testing.T) {
	fake := homeFixture()
	fake.allTracks = spotifyTracks(5)
	fake.pageLimit = 2
	m := openHome(t, newHomeTestModel(fake))
	m = pressHomeKey(t, m, tea.KeyPressMsg{Text: "j"})
	updated, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = updated.(Model)
	// Apply only the first page so the incremental chain is mid-flight.
	first, ok := cmd().(homeContentMsg)
	if !ok {
		t.Fatalf("enter on playlist produced %T; want homeContentMsg", cmd())
	}
	updated, _ = m.Update(first)
	m = updated.(Model)
	if !m.home.content.paging.active || m.home.content.paging.offset != 2 {
		t.Fatalf("paging = %+v; want active chain at offset 2", m.home.content.paging)
	}

	cmd = m.fetchProviderTracks("pl1")
	if cmd == nil {
		t.Fatal("fetchProviderTracks returned nil")
	}
	if m.home.content.paging.active {
		t.Fatal("queue load should cancel Home content paging")
	}
	msg, ok := cmd().(tracksLoadedMsg)
	if !ok || msg.playlistID != "pl1" {
		t.Fatalf("queue load produced %T; want tracksLoadedMsg for pl1", cmd())
	}
}

// TestHomeAlbumEnterLoadsTracks covers the album drill: breadcrumb, tracks,
// and the play-row-and-enqueue-remainder Enter behavior.
func TestHomeAlbumEnterLoadsTracks(t *testing.T) {
	m := openHome(t, newHomeTestModel(homeFixture()))

	// Rows: 0 "+ New playlist", 1-2 playlists, 3 Rumours.
	for range 3 {
		m = pressHomeKey(t, m, tea.KeyPressMsg{Text: "j"})
	}
	if row, ok := m.homeRowAt(); !ok || row.kind != homeRowAlbum || row.album.ID != "al1" {
		t.Fatalf("row at cursor = %+v ok=%v; want Rumours album row", row, ok)
	}
	m = pressHomeKey(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
	if m.home.content.kind != homeContentAlbum || len(m.home.content.tracks) != 2 {
		t.Fatalf("content = kind:%d tracks:%d; want album tracks", m.home.content.kind, len(m.home.content.tracks))
	}
	if crumb := m.homeContentCrumb(); crumb != "Home / Albums / Rumours" {
		t.Fatalf("crumb = %q", crumb)
	}

	// Enter on a track plays it and enqueues the remainder (artist-screen pattern).
	m = pressHomeKey(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
	if m.playlist.Len() != 2 {
		t.Fatalf("queue len = %d; want whole album queued", m.playlist.Len())
	}
	if !strings.Contains(m.status.text, "+1 queued") {
		t.Fatalf("status = %q; want remainder toast", m.status.text)
	}
}

// TestHomeArtistEnterOpensArtistScreen verifies artists open the wave-3
// artist screen on top of Home, and Esc returns to Home.
func TestHomeArtistEnterOpensArtistScreen(t *testing.T) {
	fake := homeFixture()
	fake.artistDetails = map[string]provider.ArtistDetail{
		"ar1": {Info: provider.ArtistInfo{ID: "ar1", Name: "Fleetwood Mac"}},
	}
	m := openHome(t, newHomeTestModel(fake))

	// Rows: 0 new, 1-2 playlists, 3-5 albums, 6 Fleetwood Mac.
	for range 6 {
		m = pressHomeKey(t, m, tea.KeyPressMsg{Text: "j"})
	}
	m = pressHomeKey(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
	if !m.artist.visible || !m.home.visible {
		t.Fatalf("artist = %v home = %v; want artist screen on top of Home", m.artist.visible, m.home.visible)
	}
	if m.activeScreen() != screenArtist {
		t.Fatalf("activeScreen = %v; want screenArtist on top", m.activeScreen())
	}

	// Esc pops the artist screen back to Home with it still open.
	m = pressHomeKey(t, m, tea.KeyPressMsg{Code: tea.KeyEscape})
	if m.artist.visible || !m.home.visible || m.activeScreen() != screenHome {
		t.Fatalf("artist = %v home = %v screen = %v; want back on Home", m.artist.visible, m.home.visible, m.activeScreen())
	}
}

// TestHomeNewPlaylistCreationFlow drives "+ New playlist" end to end through
// the fake PlaylistCreator: name input, creation, list refresh, and the
// fixup that lands the cursor on the new row.
func TestHomeNewPlaylistCreationFlow(t *testing.T) {
	fake := homeFixture()
	m := openHome(t, newHomeTestModel(fake))

	m = pressHomeKey(t, m, tea.KeyPressMsg{Code: tea.KeyEnter}) // "+ New playlist"
	if m.home.screen != homeScreenNewName {
		t.Fatalf("screen = %d; want name input", m.home.screen)
	}
	for _, ch := range "Mix" {
		m = pressHomeKey(t, m, tea.KeyPressMsg{Text: string(ch)})
	}
	if m.home.newName != "Mix" {
		t.Fatalf("newName = %q; want Mix", m.home.newName)
	}

	// The provider serves the created playlist on the refresh that follows.
	fake.lists = append(fake.lists, playlist.PlaylistInfo{ID: "new-Mix", Name: "Mix"})
	m = pressHomeKey(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})

	if len(fake.created) != 1 || fake.created[0] != "Mix" {
		t.Fatalf("created = %v; want Mix", fake.created)
	}
	if m.home.screen != homeScreenLibrary {
		t.Fatalf("screen = %d; want back on the library", m.home.screen)
	}
	if !strings.Contains(m.status.text, "Created") {
		t.Fatalf("status = %q; want created toast", m.status.text)
	}
	row, ok := m.homeRowAt()
	if !ok || row.kind != homeRowPlaylist || row.playlist.ID != "new-Mix" {
		t.Fatalf("cursor row = %+v ok=%v; want fixup onto the new playlist", row, ok)
	}

	m.plVisible = 12
	oldPanelWidth := ui.PanelWidth
	ui.PanelWidth = 100
	t.Cleanup(func() { ui.PanelWidth = oldPanelWidth })
	if body := m.renderHomeBody(); !strings.Contains(body, "Playlists (3)") || !strings.Contains(body, "Mix") {
		t.Fatalf("body missing refreshed playlist section")
	}
}

// TestHomeEscPopsContentThenCloses pins the back order: content drill level
// first, then the overlay itself.
func TestHomeEscPopsContentThenCloses(t *testing.T) {
	fake := homeFixture()
	fake.allTracks = spotifyTracks(2)
	m := openHome(t, newHomeTestModel(fake))

	m = pressHomeKey(t, m, tea.KeyPressMsg{Text: "j"})
	m = pressHomeKey(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
	if m.home.content.kind == homeContentNone {
		t.Fatal("setup: content should be open")
	}

	m = pressHomeKey(t, m, tea.KeyPressMsg{Code: tea.KeyEscape})
	if m.home.content.kind != homeContentNone || !m.home.visible {
		t.Fatalf("content = %d home = %v; want content popped, Home open", m.home.content.kind, m.home.visible)
	}
	if m.home.focus != homePaneSidebar {
		t.Fatal("popping content should return focus to the sidebar")
	}

	m = pressHomeKey(t, m, tea.KeyPressMsg{Code: tea.KeyEscape})
	if m.home.visible {
		t.Fatal("second Esc should close Home")
	}
}

// TestHomeHKeyClosesFromContentPane: H closes Home outright even while the
// content pane holds focus (Esc would only pop the content level).
func TestHomeHKeyClosesFromContentPane(t *testing.T) {
	fake := homeFixture()
	fake.allTracks = spotifyTracks(2)
	m := openHome(t, newHomeTestModel(fake))

	m = pressHomeKey(t, m, tea.KeyPressMsg{Text: "j"})
	m = pressHomeKey(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
	if m.home.content.kind == homeContentNone || m.home.focus != homePaneContent {
		t.Fatalf("setup: content = %d focus = %d; want content open and focused", m.home.content.kind, m.home.focus)
	}

	m = pressHomeKey(t, m, tea.KeyPressMsg{Text: "H"})
	if m.home.visible {
		t.Fatal("H should close Home from the content pane")
	}
}

// TestHomeHelpLines verifies the overlay help renders for each Home screen.
func TestHomeHelpLines(t *testing.T) {
	m := openHome(t, newHomeTestModel(homeFixture()))
	if help := m.homeHelpLine(); !strings.Contains(help, "Esc") {
		t.Fatalf("library help = %q; want Esc hint", help)
	}
	m.home.filtering = true
	if help := m.homeHelpLine(); !strings.Contains(help, "Cancel") {
		t.Fatalf("filter help = %q; want cancel hint", help)
	}
	m.home.filtering = false
	m.home.screen = homeScreenNewName
	if help := m.homeHelpLine(); !strings.Contains(help, "Confirm") {
		t.Fatalf("input help = %q; want confirm hint", help)
	}
}

// TestRegistryCoversHomeKeys keeps the new mode's reserved keys in the
// command registry so plugins cannot shadow them.
func TestRegistryCoversHomeKeys(t *testing.T) {
	for _, key := range []string{"H", "s", "S", "/", "1", "2", "3", "tab", "esc", "enter", "*"} {
		found := false
		for _, c := range commandRegistry {
			if c.Mode&commandModeHome == 0 {
				continue
			}
			for _, k := range c.Keys {
				if k == key {
					found = true
				}
			}
		}
		if !found {
			t.Errorf("commandRegistry has no %q entry for commandModeHome", key)
		}
	}
	found := false
	for _, c := range commandRegistry {
		if c.Mode == commandModeMain {
			for _, k := range c.Keys {
				if k == "H" {
					found = true
				}
			}
		}
	}
	if !found {
		t.Error("commandRegistry has no H entry for commandModeMain")
	}
}

// TestHomeSectionJumpKeys: 1/2/3 snap the sidebar cursor to the first row of
// Playlists / Albums / Artists — the long-scroll fix for one-flat-list nav.
func TestHomeSectionJumpKeys(t *testing.T) {
	fake := homeFixture()
	m := openHome(t, newHomeTestModel(fake))

	rows := m.homeRows()
	firstOf := func(kind homeRowKind) int {
		for i, r := range rows {
			if r.kind == kind {
				return i
			}
		}
		return -1
	}

	m = pressHomeKey(t, m, tea.KeyPressMsg{Text: "3"})
	if want := firstOf(homeRowArtist); m.home.cursor != want {
		t.Fatalf("cursor = %d; want first artist row %d", m.home.cursor, want)
	}
	m = pressHomeKey(t, m, tea.KeyPressMsg{Text: "1"})
	if want := firstOf(homeRowPlaylist); m.home.cursor != want {
		t.Fatalf("cursor = %d; want first playlist row %d", m.home.cursor, want)
	}
	m = pressHomeKey(t, m, tea.KeyPressMsg{Text: "2"})
	if want := firstOf(homeRowAlbum); m.home.cursor != want {
		t.Fatalf("cursor = %d; want first album row %d", m.home.cursor, want)
	}
}

// TestHomeSectionGensIndependent pins the per-section generation counters:
// a lazy album page or creation refetch must not make an in-flight
// playlists/artists completion stale, which would wedge its loading flag.
func TestHomeSectionGensIndependent(t *testing.T) {
	fake := homeFixture()
	m := newHomeTestModel(fake)

	cmd := m.openHomeView()
	batch, ok := cmd().(tea.BatchMsg)
	if !ok {
		t.Fatalf("open produced %T; want a batch of section fetches", cmd())
	}
	var listsMsg homeListsMsg
	for _, sub := range batch {
		if msg, ok := sub().(homeListsMsg); ok {
			listsMsg = msg
		}
	}

	// Simulate a lazy album page superseding only the albums generation.
	nextRequest(&m.requests.homeAlbums)

	updated, _ := m.Update(listsMsg)
	m = updated.(Model)
	if m.home.loadingLists {
		t.Fatal("lists completion dropped by another section's generation — loading flag wedged")
	}
	if len(m.home.lists) == 0 {
		t.Fatal("lists completion was not applied")
	}
}

// TestHomeNewPlaylistEnterGuardBlocksDuplicateCreate: Enter while a create
// request is in flight must not dispatch a second one.
func TestHomeNewPlaylistEnterGuardBlocksDuplicateCreate(t *testing.T) {
	fake := homeFixture()
	m := openHome(t, newHomeTestModel(fake))

	m = pressHomeKey(t, m, tea.KeyPressMsg{Code: tea.KeyEnter}) // "+ New playlist"
	for _, ch := range "Mix" {
		m = pressHomeKey(t, m, tea.KeyPressMsg{Text: string(ch)})
	}

	// Dispatch the create and run it, but keep its completion unapplied so
	// the creating flag stays set (in flight).
	updated, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = updated.(Model)
	if !m.home.creating {
		t.Fatal("creating flag not set on dispatch")
	}
	if cmd == nil {
		t.Fatal("no create command dispatched")
	}
	_ = cmd() // runs CreatePlaylist on the fake; completion not applied
	if len(fake.created) != 1 {
		t.Fatalf("created = %v; want one create", fake.created)
	}

	updated, cmd2 := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = updated.(Model)
	if cmd2 != nil {
		t.Fatal("second Enter while in flight dispatched another create")
	}
	if len(fake.created) != 1 {
		t.Fatalf("created = %v; want still one create", fake.created)
	}
}
