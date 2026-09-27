package model

import (
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

// immersiveModel builds a Model with immersive state populated directly —
// tests exercise pure logic and rendering without a live provider.
func immersiveModel(t *testing.T) *Model {
	t.Helper()
	old := ui.PanelWidth
	ui.PanelWidth = 80
	t.Cleanup(func() { ui.PanelWidth = old })

	m := &Model{playlist: playlist.New(), width: 120, height: 34}
	m.recomputeLayout()
	m.immersive = immersiveState{
		active: true,
		focus:  immPaneCenter,
		zone:   zoneRolo,
		view:   immViewHome,
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

func TestImmersiveRailRows(t *testing.T) {
	tests := []struct {
		name     string
		section  immersiveRailSection
		filter   string
		sort     immRailSort
		wantName []string
	}{
		{"playlists recents", immSectionPlaylists, "", immRailSortRecents,
			[]string{"Stuff to listen 2", "kanyesTimelessSound", "Liked Songs"}},
		{"playlists alpha", immSectionPlaylists, "", immRailSortAlpha,
			[]string{"kanyesTimelessSound", "Liked Songs", "Stuff to listen 2"}},
		{"playlists filtered", immSectionPlaylists, "liked", immRailSortRecents,
			[]string{"Liked Songs"}},
		{"albums", immSectionAlbums, "", immRailSortRecents,
			[]string{"Charm", "BRAT"}},
		{"artists", immSectionArtists, "", immRailSortRecents,
			[]string{"Lizzo", "Frankie Valli & The Four Seasons"}},
		{"artists filtered by kind", immSectionArtists, "lizzo", immRailSortRecents,
			[]string{"Lizzo"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := immersiveModel(t)
			m.immersive.railSection = tt.section
			m.immersive.railFilter = tt.filter
			m.immersive.railSort = tt.sort
			rows := m.railRows()
			if len(rows) != len(tt.wantName) {
				t.Fatalf("railRows() = %d rows, want %d", len(rows), len(tt.wantName))
			}
			for i, want := range tt.wantName {
				if rows[i].name != want {
					t.Errorf("railRows()[%d].name = %q, want %q", i, rows[i].name, want)
				}
			}
		})
	}
}

func TestImmersiveRoloSpin(t *testing.T) {
	tests := []struct {
		name       string
		cursor     int
		kicks      []int
		steps      int // steps to consume; -1 = all
		wantCursor int
	}{
		{"single kick right", 0, []int{1}, -1, 2},
		{"single kick left wraps", 0, []int{-1}, -1, 1},
		{"three kicks momentum", 0, []int{1, 1, 1}, -1, 0},
		{"reverse cancels then spins left", 0, []int{1, -1}, -1, 2},
		{"partial consumption", 0, []int{1}, 1, 2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := immersiveModel(t)
			m.immersive.roloCursor = tt.cursor
			for _, dir := range tt.kicks {
				m.roloSpinKick(dir)
			}
			steps := tt.steps
			if steps < 0 {
				steps = 100
			}
			for i := 0; i < steps && m.immersive.roloSpin > 0; i++ {
				m.roloSpinStep()
			}
			if got := m.immersive.roloCursor; got != tt.wantCursor {
				t.Errorf("roloCursor = %d, want %d", got, tt.wantCursor)
			}
		})
	}
}

func TestImmersiveRoloSpinEmptyDeck(t *testing.T) {
	m := immersiveModel(t)
	m.immersive.view = immViewPlaylist
	m.immersive.roloMode = true
	m.immersive.tracks = nil
	m.roloSpinKick(1)
	if m.immersive.roloSpin != 0 {
		t.Errorf("empty deck still queued %d spin steps", m.immersive.roloSpin)
	}
}

func TestImmersiveSortedTracks(t *testing.T) {
	m := immersiveModel(t)
	m.immersive.tracks = []playlist.Track{
		{Title: "Banana", Album: "B", DurationSecs: 300},
		{Title: "apple", Album: "a", DurationSecs: 10},
		{Title: "Cherry", Album: "C", DurationSecs: 200},
	}
	tests := []struct {
		sort  immersiveTrackSort
		want0 string
		want2 string
	}{
		{immSortTrackOrder, "Banana", "Cherry"},
		{immSortTrackTitle, "apple", "Cherry"},
		{immSortTrackAlbum, "apple", "Cherry"},
		{immSortTrackDuration, "apple", "Banana"},
	}
	for _, tt := range tests {
		t.Run(immSortTrackLabels[tt.sort], func(t *testing.T) {
			m.immersive.trackSort = tt.sort
			got := m.sortedTracks()
			if got[0].Title != tt.want0 || got[2].Title != tt.want2 {
				t.Errorf("sorted order = %q,%q,%q; want %q first, %q last",
					got[0].Title, got[1].Title, got[2].Title, tt.want0, tt.want2)
			}
			// Source list must be untouched.
			if m.immersive.tracks[0].Title != "Banana" {
				t.Errorf("source order mutated: %q", m.immersive.tracks[0].Title)
			}
		})
	}
}

func TestImmersiveEscapeLayers(t *testing.T) {
	tests := []struct {
		name       string
		view       immersiveView
		roloMode   bool
		back       int
		wantView   immersiveView
		wantRolo   bool
		wantActive bool
	}{
		{"rolodex closes first", immViewPlaylist, true, 1, immViewPlaylist, false, true},
		{"back restores previous view", immViewPlaylist, false, 1, immViewPlaylist, false, true},
		{"no back at playlist goes home", immViewPlaylist, false, 0, immViewHome, false, true},
		{"home exits the mode", immViewHome, false, 0, immViewHome, false, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := immersiveModel(t)
			m.immersive.view = tt.view
			m.immersive.roloMode = tt.roloMode
			for i := 0; i < tt.back; i++ {
				m.immersive.back = append(m.immersive.back, immNavSnap{view: immViewPlaylist})
			}
			m.immersiveEscape()
			if m.immersive.view != tt.wantView || m.immersive.roloMode != tt.wantRolo || m.immersive.active != tt.wantActive {
				t.Errorf("after esc: view=%d rolo=%v active=%v; want %d %v %v",
					m.immersive.view, m.immersive.roloMode, m.immersive.active,
					tt.wantView, tt.wantRolo, tt.wantActive)
			}
		})
	}
}

func TestImmersiveBackStackRestoresContext(t *testing.T) {
	m := immersiveModel(t)
	m.immersive.view = immViewPlaylist
	m.immersive.ctxID, m.immersive.ctxName = "pl1", "Stuff to listen 2"
	m.immersive.tracks = []playlist.Track{{Title: "One"}, {Title: "Two"}}
	m.immersive.trackCursor = 1

	m.pushImmersiveBack()
	m.immersive.view = immViewSearch
	m.immersive.ctxID = ""
	m.immersive.tracks = nil

	m.immersiveGoBack()
	if m.immersive.view != immViewPlaylist || m.immersive.ctxID != "pl1" ||
		len(m.immersive.tracks) != 2 || m.immersive.trackCursor != 1 {
		t.Errorf("back did not restore context: %+v", m.immersive.ctxID)
	}
}

func TestImmersivePaneWidths(t *testing.T) {
	tests := []struct {
		width     int
		collapsed bool
		wantRail  int // 0 = hidden
		wantRight int // 0 = hidden
	}{
		{140, false, immRailMaxW, immRightMaxW},
		{110, false, 27, immRightMaxW},
		{90, false, 22, immRightMinW},
		{70, false, immRailMinW, 0},
		{60, false, 0, 0},
		{140, true, immRailIconW, immRightMaxW},
		{60, true, immRailIconW, 0},
	}
	for _, tt := range tests {
		name := "w=" + strconv.Itoa(tt.width)
		if tt.collapsed {
			name += "+collapsed"
		}
		t.Run(name, func(t *testing.T) {
			m := immersiveModel(t)
			m.immersive.railCollapsed = tt.collapsed
			if got := m.immersiveRailWidth(tt.width); got != tt.wantRail {
				t.Errorf("immersiveRailWidth(%d) = %d, want %d", tt.width, got, tt.wantRail)
			}
			if got := m.immersiveRightWidth(tt.width); got != tt.wantRight {
				t.Errorf("immersiveRightWidth(%d) = %d, want %d", tt.width, got, tt.wantRight)
			}
		})
	}
}

func TestImmersiveFitCell(t *testing.T) {
	tests := []struct {
		in   string
		w    int
		want string
	}{
		{"abc", 5, "abc  "},
		{"abcdef", 4, "abcd"},
		{"", 3, "   "},
		{"x", 0, ""},
	}
	for _, tt := range tests {
		if got := fitCell(tt.in, tt.w); got != tt.want {
			t.Errorf("fitCell(%q, %d) = %q, want %q", tt.in, tt.w, got, tt.want)
		}
	}
}

func TestImmersiveHandleKeyBasics(t *testing.T) {
	tests := []struct {
		name       string
		key        string
		wantFocus  immersivePane
		wantActive bool
	}{
		{"tab moves center→right", "tab", immPaneRight, true},
		{"shift+tab moves center→rail", "shift+tab", immPaneRail, true},
		{"I exits", "I", immPaneRail, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := immersiveModel(t)
			m.handleImmersiveKey(keyMsg(tt.key))
			if m.immersive.focus != tt.wantFocus || m.immersive.active != tt.wantActive {
				t.Errorf("key %q: focus=%d active=%v; want %d %v",
					tt.key, m.immersive.focus, m.immersive.active, tt.wantFocus, tt.wantActive)
			}
		})
	}
}

func TestImmersiveSectionKeys(t *testing.T) {
	m := immersiveModel(t)
	for _, key := range []string{"2", "3", "1"} {
		m.handleImmersiveKey(keyMsg(key))
	}
	if m.immersive.railSection != immSectionPlaylists {
		t.Errorf("railSection = %d after 1, want playlists", m.immersive.railSection)
	}
	m.handleImmersiveKey(keyMsg("3"))
	if m.immersive.railSection != immSectionArtists {
		t.Errorf("railSection = %d after 3, want artists", m.immersive.railSection)
	}
}

func TestImmersiveTrackSortKey(t *testing.T) {
	m := immersiveModel(t)
	m.immersive.view = immViewPlaylist
	m.immersive.zone = zoneTable
	m.handleImmersiveKey(keyMsg("t"))
	if m.immersive.trackSort != immSortTrackTitle {
		t.Errorf("trackSort = %d, want title", m.immersive.trackSort)
	}
	m.handleImmersiveKey(keyMsg("t"))
	if m.immersive.trackSort != immSortTrackAlbum {
		t.Errorf("trackSort = %d, want album", m.immersive.trackSort)
	}
}

func TestImmersiveRoloToggle(t *testing.T) {
	m := immersiveModel(t)
	m.immersive.view = immViewPlaylist
	m.handleImmersiveKey(keyMsg("o"))
	if !m.immersive.roloMode || m.immersive.zone != zoneRolo {
		t.Errorf("o did not open rolodex: mode=%v zone=%d", m.immersive.roloMode, m.immersive.zone)
	}
	m.handleImmersiveKey(keyMsg("o"))
	if m.immersive.roloMode {
		t.Error("o did not close rolodex")
	}
}

func TestImmersiveRenderFrameShape(t *testing.T) {
	m := immersiveModel(t)
	m.width, m.height = 120, 34
	m.recomputeLayout()
	out := stripAnsi(m.renderImmersive())
	lines := strings.Split(out, "\n")
	if len(lines) < 20 {
		t.Fatalf("immersive frame = %d rows", len(lines))
	}
	// Top bar carries the search field; the player bar carries transport.
	joined := strings.Join(lines, "\n")
	for _, want := range []string{"what do you want to play", "Your Library", "Playlists", "Jump back in"} {
		if !strings.Contains(joined, want) {
			t.Errorf("frame missing %q\ngot:\n%s", want, joined)
		}
	}
}

func TestImmersiveRolodexStripHasNeighbors(t *testing.T) {
	m := immersiveModel(t)
	m.immersive.roloCursor = 1
	strip := strings.Join(m.renderImmRolodexStrip(80), "\n")
	// Focused card plus left/right neighbors all render (neighbors truncated).
	for _, want := range []string{"Stuff t", "kanyesTimelessSound", "Liked S", "◂ 2/3 ▸"} {
		if !strings.Contains(strip, want) {
			t.Errorf("rolodex strip missing %q\ngot:\n%s", want, strip)
		}
	}
}

func TestImmersiveTableRowsHighlight(t *testing.T) {
	m := immersiveModel(t)
	m.immersive.view = immViewPlaylist
	m.immersive.tracks = []playlist.Track{
		{Title: "One", Album: "A", DurationSecs: 60},
		{Title: "Two", Album: "B", DurationSecs: 120},
	}
	rows := strings.Join(m.immTableRows(60, 10, m.sortedTracks()), "\n")
	for _, want := range []string{"One", "Two", "1:00", "2:00"} {
		if !strings.Contains(rows, want) {
			t.Errorf("table missing %q\ngot:\n%s", want, rows)
		}
	}
}

func TestImmersiveEnterExitRestoresNothing(t *testing.T) {
	m := immersiveModel(t)
	m.immersive = immersiveState{} // simulate normal mode
	m.immersive.active = false

	// Exiting a mode that was never entered must not corrupt state.
	m.exitImmersive()
	if m.immersive.active {
		t.Error("exitImmersive left active=true")
	}

	m.provider = nil
	if cmd := m.enterImmersive(); cmd != nil {
		t.Error("enterImmersive with no provider returned a fetch cmd")
	}
	if !m.immersive.active || m.immersive.view != immViewHome {
		t.Errorf("enterImmersive: active=%v view=%d", m.immersive.active, m.immersive.view)
	}
}

func TestImmersiveGridMove(t *testing.T) {
	m := immersiveModel(t)
	m.immersive.zone = zoneGrid
	m.immersive.gridCursor = 0
	// gridCols depends on width; with 3 items one row covers all.
	cols := m.immersiveGridCols()
	if cols < 1 {
		t.Fatalf("immersiveGridCols = %d", cols)
	}
	m.immersiveMoveCenter(0, 1)
	if m.immersive.gridCursor != 1 {
		t.Errorf("gridCursor = %d after l, want 1", m.immersive.gridCursor)
	}
	m.immersiveMoveCenter(0, -1)
	if m.immersive.gridCursor != 0 {
		t.Errorf("gridCursor = %d after h, want 0", m.immersive.gridCursor)
	}
	m.immersiveMoveCenter(-1, 0)
	if m.immersive.zone != zoneRolo {
		t.Errorf("up on top grid row did not return to rolodex (zone=%d)", m.immersive.zone)
	}
}

func TestImmersiveTableStaysInTable(t *testing.T) {
	m := immersiveModel(t)
	m.immersive.view = immViewPlaylist
	m.immersive.zone = zoneTable
	m.immersiveMoveCenter(0, -1) // h on a track table: no grid zone exists here
	if m.immersive.zone != zoneTable {
		t.Errorf("h on playlist table dropped to zone=%d, want zoneTable", m.immersive.zone)
	}
	m.immersive.zone = zoneRolo
	m.immersiveMoveCenter(1, 0) // j on the deck strip of a non-home view must not enter grid
	if m.immersive.zone != zoneRolo {
		t.Errorf("j off the deck leaked to zone=%d, want zoneRolo", m.immersive.zone)
	}
}
