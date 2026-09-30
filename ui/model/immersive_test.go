package model

import (
	"context"
	"strconv"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/bjarneo/cliamp/playlist"
	"github.com/bjarneo/cliamp/provider"
	"github.com/bjarneo/cliamp/ui"
)

// keyMsg builds a KeyPressMsg whose String() is s for the keys the tests use.
func keyMsg(s string) tea.KeyPressMsg {
	switch s {
	case "tab":
		return tea.KeyPressMsg{Code: tea.KeyTab}
	case "shift+tab":
		return tea.KeyPressMsg{Code: tea.KeyTab, Mod: tea.ModShift}
	case "enter":
		return tea.KeyPressMsg{Code: tea.KeyEnter}
	case "esc":
		return tea.KeyPressMsg{Code: tea.KeyEscape}
	default:
		return tea.KeyPressMsg{Text: s}
	}
}

// immProvStub is a minimal playlist.Provider for browse/open tests.
type immProvStub struct{}

func (immProvStub) Name() string { return "stub" }

func (immProvStub) Playlists() ([]playlist.PlaylistInfo, error) { return nil, nil }

func (immProvStub) Tracks(string) ([]playlist.Track, error) {
	return []playlist.Track{
		{Title: "Song A", Artist: "Artist", Album: "Alb", DurationSecs: 61, Path: "/a"},
		{Title: "Song B", Artist: "Artist", Album: "Alb", DurationSecs: 62, Path: "/b"},
	}, nil
}

func (immProvStub) SearchTracks(_ context.Context, query string, _ int) ([]playlist.Track, error) {
	return []playlist.Track{
		{Title: "Found " + query, Artist: "Artist", Path: "/found"},
	}, nil
}

// immSubStub adds SubscriptionLister for the Podcasts pill.
type immSubStub struct{ immProvStub }

func (immSubStub) Subscriptions() []provider.SubscriptionInfo {
	return []provider.SubscriptionInfo{
		{ID: "pd1", Name: "Some Show", Author: "Some Author"},
	}
}

// immersiveModel builds a Model with immersive state populated directly —
// tests exercise pure logic and rendering without a live provider.
func immersiveModel(t testing.TB) *Model {
	t.Helper()
	old := ui.PanelWidth
	ui.PanelWidth = 80
	t.Cleanup(func() { ui.PanelWidth = old })

	m := &Model{playlist: playlist.New(), width: 120, height: 34}
	m.recomputeLayout()
	m.immersive = immersiveState{
		active:  true,
		prov:    immProvStub{},
		focus:   immPaneCanvas,
		section: immSecPlaylists,
		view:    immViewBrowse,
		mode:    immCanvasList,
		lists: []playlist.PlaylistInfo{
			{ID: "pl1", Name: "Stuff to listen 2", TrackCount: 12},
			{ID: "pl2", Name: "kanyesTimelessSound", TrackCount: 40},
			{ID: "pl3", Name: "Liked Songs", TrackCount: 500},
		},
		albums: []provider.AlbumInfo{
			{ID: "al1", Name: "Charm", Artist: "Clairo", Year: 2024},
			{ID: "al2", Name: "BRAT", Artist: "Charli xcx", Year: 2024},
		},
		artists: []provider.ArtistInfo{
			{ID: "ar1", Name: "Lizzo"},
			{ID: "ar2", Name: "Frankie Valli & The Four Seasons"},
		},
	}
	return m
}

func TestImmersiveBrowseItems(t *testing.T) {
	m := immersiveModel(t)
	tests := []struct {
		name     string
		section  immSection
		filter   string
		sort     immBrowseSort
		wantName []string
	}{
		{"playlists recents", immSecPlaylists, "", immBrowseSortRecents,
			[]string{"Stuff to listen 2", "kanyesTimelessSound", "Liked Songs"}},
		{"playlists alpha", immSecPlaylists, "", immBrowseSortAlpha,
			[]string{"kanyesTimelessSound", "Liked Songs", "Stuff to listen 2"}},
		{"playlists filtered", immSecPlaylists, "liked", immBrowseSortRecents,
			[]string{"Liked Songs"}},
		{"albums", immSecAlbums, "", immBrowseSortRecents,
			[]string{"Charm", "BRAT"}},
		{"artists", immSecArtists, "", immBrowseSortRecents,
			[]string{"Lizzo", "Frankie Valli & The Four Seasons"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m.immersive.section = tt.section
			m.immersive.filter = tt.filter
			m.immersive.sort = tt.sort
			items := m.browseItems()
			if len(items) != len(tt.wantName) {
				t.Fatalf("len(items) = %d, want %d", len(items), len(tt.wantName))
			}
			for i, it := range items {
				if it.title != tt.wantName[i] {
					t.Fatalf("items[%d] = %q, want %q", i, it.title, tt.wantName[i])
				}
			}
		})
	}
}

func TestImmersiveBrowseItemsPodcasts(t *testing.T) {
	m := immersiveModel(t)
	m.immersive.prov = immSubStub{}
	m.immersive.section = immSecPodcasts
	items := m.browseItems()
	if len(items) != 1 || items[0].title != "Some Show" || items[0].sub != "Some Author" {
		t.Fatalf("podcast items = %+v", items)
	}
	if items[0].kind != immKindShow {
		t.Fatalf("kind = %d, want show", items[0].kind)
	}
}

func TestImmersiveDigitKeysSelectSections(t *testing.T) {
	m := immersiveModel(t)
	for i, want := range immSectionOrder {
		m.handleImmersiveKey(keyMsg(strconv.Itoa(i + 1)))
		if want == immSecSearch && m.immersive.searching {
			// digits typed into the search field are input, not pill shortcuts
			m.handleImmersiveKey(keyMsg("esc"))
		}
		if m.immersive.section != want {
			t.Fatalf("key %d: section = %d, want %d", i+1, m.immersive.section, want)
		}
		if m.immersive.view != immRootView(want) {
			t.Fatalf("key %d: view = %d, want %d", i+1, m.immersive.view, immRootView(want))
		}
	}
}

func TestImmersiveModeCycles(t *testing.T) {
	m := immersiveModel(t)
	for i := 0; i < int(immCanvasModeCount); i++ {
		want := immCanvasMode((int(immCanvasList) + i) % int(immCanvasModeCount))
		if m.immersive.mode != want {
			t.Fatalf("step %d: mode = %d, want %d", i, m.immersive.mode, want)
		}
		m.handleImmersiveKey(keyMsg("c"))
	}
}

func TestImmersiveOpenPlaylistSetsContext(t *testing.T) {
	m := immersiveModel(t)
	items := m.browseItems()
	cmd := m.openImmersiveItem(items[0])
	if m.immersive.view != immViewPlaylist {
		t.Fatalf("view = %d, want playlist", m.immersive.view)
	}
	if !m.immersive.tracksLoading {
		t.Fatal("expected tracksLoading")
	}
	if m.immersive.ctxID != "pl1" {
		t.Fatalf("ctxID = %q, want pl1", m.immersive.ctxID)
	}
	if cmd == nil {
		t.Fatal("expected a fetch cmd")
	}
	msg := cmd()
	content, ok := msg.(immersiveContentMsg)
	if !ok {
		t.Fatalf("cmd returned %T, want immersiveContentMsg", msg)
	}
	if len(content.tracks) != 2 {
		t.Fatalf("fetched %d tracks, want 2", len(content.tracks))
	}
}

func TestImmersiveBackStack(t *testing.T) {
	m := immersiveModel(t)
	items := m.browseItems()
	m.openImmersiveItem(items[0])
	m.immersive.tracksLoading = false
	m.immersive.tracks = []playlist.Track{{Title: "t", Path: "/t"}}

	m.pushImmersiveBack()
	m.immersive.view = immViewAlbum
	m.immersiveEscape()
	if m.immersive.view != immViewPlaylist {
		t.Fatalf("esc: view = %d, want playlist", m.immersive.view)
	}
	m.immersiveGoBack()
	if m.immersive.view != immViewBrowse {
		t.Fatalf("back to root: view = %d, want browse", m.immersive.view)
	}
	m.immersiveEscape()
	if m.immersive.active {
		t.Fatal("esc at root should exit immersive mode")
	}
}

func TestImmersiveSettingsTab(t *testing.T) {
	m := immersiveModel(t)
	m.handleImmersiveKey(keyMsg("e"))
	if m.immersive.view != immViewSettings {
		t.Fatalf("view = %d, want settings", m.immersive.view)
	}
	m.immersive.view = immViewPlaylist
	m.immersive.settingsReturn = immViewPlaylist
	m.immersive.view = immViewSettings
	m.handleImmersiveKey(keyMsg("esc"))
	if m.immersive.view != immViewPlaylist {
		t.Fatalf("esc: view = %d, want playlist", m.immersive.view)
	}
}

func TestImmersiveSortedTracks(t *testing.T) {
	m := immersiveModel(t)
	m.immersive.tracks = []playlist.Track{
		{Title: "Zed", DurationSecs: 60, Path: "/z"},
		{Title: "Ann", DurationSecs: 30, Path: "/a"},
	}
	got := m.sortedTracks()
	if got[0].Title != "Zed" {
		t.Fatal("track-order sort must keep playlist order")
	}
	m.immersive.trackSort = immSortTrackTitle
	if got := m.sortedTracks(); got[0].Title != "Ann" {
		t.Fatalf("title sort: first = %q, want Ann", got[0].Title)
	}
	m.immersive.trackSort = immSortTrackDuration
	if got := m.sortedTracks(); got[0].DurationSecs != 30 {
		t.Fatalf("duration sort: first = %v", got[0].DurationSecs)
	}
}

func TestImmersiveMoveSnap(t *testing.T) {
	m := immersiveModel(t)
	m.immersive.tracks = make([]playlist.Track, 30)
	for i := range m.immersive.tracks {
		m.immersive.tracks[i] = playlist.Track{Title: "t" + strconv.Itoa(i)}
	}
	m.immersive.view = immViewPlaylist
	m.handleImmersiveKey(keyMsg("j"))
	if m.immersive.cursor != 1 {
		t.Fatalf("cursor = %d, want 1", m.immersive.cursor)
	}
	m.handleImmersiveKey(keyMsg("pgdown"))
	if m.immersive.cursor < 3 {
		t.Fatalf("cursor = %d, want page step", m.immersive.cursor)
	}
	m.handleImmersiveKey(keyMsg("k"))
	m.handleImmersiveKey(keyMsg("shift+home"))
	if m.immersive.cursor != 0 {
		t.Fatalf("home: cursor = %d, want 0", m.immersive.cursor)
	}
}

func TestImmersiveFrameShape(t *testing.T) {
	m := immersiveModel(t)
	out := stripAnsi(m.renderImmersive())
	lines := strings.Split(out, "\n")
	g := m.immGeom()
	if len(lines) != g.h {
		t.Fatalf("lines = %d, want %d", len(lines), g.h)
	}
	for _, want := range []string{"Playlists", "Search", "Podcasts", "Now Playing", "Queue"} {
		if !strings.Contains(out, want) {
			t.Fatalf("frame missing %q", want)
		}
	}
	if !strings.Contains(lines[g.navY], "╭") {
		t.Fatal("nav row lacks pill borders")
	}
	if !strings.Contains(lines[g.seekY], immBarTrack) && !strings.Contains(lines[g.seekY], "0:00") {
		t.Fatal("progress row missing")
	}
}

func TestImmersiveTooSmallFallsBack(t *testing.T) {
	m := immersiveModel(t)
	m.player = &playbackFakeEngine{}
	m.mouse = &mouseState{}
	m.immMouse = &immMouseGeom{}
	m.width, m.height = 70, 20
	m.recomputeLayout()
	if m.activeScreen() == screenImmersive {
		t.Fatal("immersive shown below min size")
	}
	out := m.View().Content
	if strings.Contains(stripAnsi(out), "Now Playing") {
		t.Fatal("immersive frame drew while too small")
	}
	// I still exits immersive state on a too-small terminal.
	m.handleKey(keyMsg("I"))
	if m.immersive.active {
		t.Fatal("I did not exit immersive")
	}
}

func TestImmersiveArtRectsExposed(t *testing.T) {
	m := immersiveModel(t)
	m.mouse = &mouseState{}
	m.immMouse = &immMouseGeom{}
	m.View()
	rects := m.ImmersiveArtRects()
	if len(rects) == 0 {
		t.Fatal("no art rects recorded")
	}
	g := m.immGeom()
	if rects[0].W != g.npArt.W || rects[0].H != g.npArt.H {
		t.Fatalf("first art rect = %+v, want now-playing %+v", rects[0], g.npArt)
	}
}

func TestImmersiveGlyphSets(t *testing.T) {
	m := immersiveModel(t)
	m.player = &playbackFakeEngine{playing: true}
	g := m.immGeom()
	plain := stripAnsi(strings.Join(m.renderImmControls(g), "\n"))
	m.nerdFontGlyphs = true
	nerd := stripAnsi(strings.Join(m.renderImmControls(g), "\n"))
	if !strings.Contains(plain, immGlyphsUnicode.pause) {
		t.Fatal("unicode pause glyph missing")
	}
	if !strings.Contains(nerd, "\uf04c") {
		t.Fatal("nerd font pause glyph missing")
	}
}

func TestImmersiveSearchFlow(t *testing.T) {
	m := immersiveModel(t)
	m.handleImmersiveKey(keyMsg("/"))
	if !m.immersive.searching || m.immersive.section != immSecSearch {
		t.Fatalf("search not open: %+v", m.immersive)
	}
	for _, r := range "lizzo" {
		m.handleImmersiveKey(tea.KeyPressMsg{Text: string(r)})
	}
	if m.immersive.searchQuery != "lizzo" {
		t.Fatalf("query = %q", m.immersive.searchQuery)
	}
	// no Searcher on the stub: enter just closes the input.
	m.handleImmersiveKey(keyMsg("enter"))
	if m.immersive.searching {
		t.Fatal("enter did not close search input")
	}
	if m.immersive.view != immViewSearch || !m.immersive.searchLoading {
		t.Fatalf("view = %d loading=%v, want search view", m.immersive.view, m.immersive.searchLoading)
	}
}

func TestImmersiveQueuePanel(t *testing.T) {
	m := immersiveModel(t)
	tracks := []playlist.Track{
		{Title: "one", Path: "/1"}, {Title: "two", Path: "/2"}, {Title: "three", Path: "/3"},
	}
	for i := range tracks {
		m.playlist.Add(tracks[i])
	}
	m.playlist.Queue(1)
	m.playlist.Queue(2)
	g := m.immGeom()
	lines := m.renderImmQueue(g)
	out := stripAnsi(strings.Join(lines, "\n"))
	if !strings.Contains(out, " 1 two") || !strings.Contains(out, " 2 three") {
		t.Fatalf("queue rows missing: %q", out)
	}
}

func TestImmVisRowsFor(t *testing.T) {
	tests := []struct {
		name string
		h    int
		want int
	}{
		{"floor on short frames", 26, immVisMinRows},
		{"about a fifth", 40, 8},
		{"ceiling on tall frames", 90, immVisMaxRows},
		{"body keeps its minimum", 27, 27 - immFixedRows - immBodyMinH},
		{"never below the floor", 20, immVisMinRows},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := immVisRowsFor(tt.h); got != tt.want {
				t.Fatalf("immVisRowsFor(%d) = %d, want %d", tt.h, got, tt.want)
			}
		})
	}
}

func TestImmersiveArtIsSquare(t *testing.T) {
	for _, sz := range [][2]int{{120, 34}, {160, 40}, {200, 50}} {
		m := immersiveModel(t)
		m.width, m.height = sz[0], sz[1]
		m.recomputeLayout()
		m.immersive.mode = immCanvasGrid
		g := m.immGeom()
		if a := g.npArt; a.H > 0 && a.W != 2*a.H {
			t.Fatalf("%dx%d: now playing art %dx%d cells, want width 2*height", sz[0], sz[1], a.W, a.H)
		}
		for _, it := range m.immCanvasItemsGeom(g.canvasIW, g.canvasIH) {
			if a := it.art; a.H > 0 && a.W != 2*a.H && a.H == it.box.H-2 {
				t.Fatalf("%dx%d: grid art %dx%d cells, want width 2*height", sz[0], sz[1], a.W, a.H)
			}
		}
	}
}

func TestImmersiveQueuePanelShowsUpcoming(t *testing.T) {
	m := immersiveModel(t)
	m.playlist.Replace([]playlist.Track{
		{Title: "One", Path: "/1"}, {Title: "Two", Path: "/2"},
		{Title: "Three", Path: "/3"}, {Title: "Four", Path: "/4"},
	})
	m.playlist.SetIndex(0)
	m.playlist.Queue(3)
	rows := m.immQueueRows()
	var got []string
	for _, r := range rows {
		got = append(got, r.Track.Title)
	}
	want := []string{"Four", "Two", "Three", "Four"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("queue rows = %v, want %v (queued first, then play order)", got, want)
	}
	m.player = &playbackFakeEngine{}
	m.vis = ui.NewVisualizer(44100)
	m.SetVisualizer("none")
	m.immersive.queueCursor = 1
	m.immersiveQueueJump()
	if cur, _ := m.playlist.Current(); cur.Title != "Two" {
		t.Fatalf("jump to an upcoming row played %q, want Two", cur.Title)
	}
}
