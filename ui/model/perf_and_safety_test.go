package model

import (
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/bjarneo/cliamp/playlist"
	"github.com/bjarneo/cliamp/ui"
)

func TestLuaSelectEmptyOptionsAndCtrlC(t *testing.T) {
	canceled := false
	m := Model{
		player:   &playbackFakeEngine{},
		playlist: playlist.New(),
	}
	updated, _ := m.Update(LuaSelectMsg{
		Title:    "Empty",
		Options:  nil,
		OnCancel: func() { canceled = true },
	})
	m = updated.(Model)
	if !canceled {
		t.Fatal("empty LuaSelectMsg did not call OnCancel")
	}
	if m.luaSelect != nil {
		t.Fatal("empty LuaSelectMsg left modal open")
	}

	m.luaSelect = &luaSelectState{
		title:   "Pick",
		options: []string{"One", "Two"},
	}
	updated, cmd := m.Update(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
	m = updated.(Model)
	if !m.quitting || cmd == nil {
		t.Fatalf("ctrl+c while luaSelect open: quitting=%t cmd=%v, want quit", m.quitting, cmd)
	}
}

func TestNilProviderEscapeDoesNotFocusProvider(t *testing.T) {
	m := Model{
		playlist: playlist.New(),
		focus:    focusPlaylist,
	}
	updated, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	m = updated.(Model)
	if m.focus == focusProvider {
		t.Fatal("esc focused nil provider")
	}
}

func TestBuiltinVisualizerWidthMatchesPaneSplit(t *testing.T) {
	panes := ui.NewPaneSet()
	if err := panes.Add(string(ui.RegionVisualizer), "cover", 1); err != nil {
		t.Fatal(err)
	}
	m := Model{
		width:  106,
		height: 30,
		vis:    ui.NewVisualizer(44100),
	}
	m.SetPaneSet(panes)
	m.recomputeLayout()

	// Default paddingH is 3, so panelWidth is 100 and a 1:1 split gives 50 cols.
	if m.vis.Cols != 50 {
		t.Fatalf("vis.Cols = %d, want 50", m.vis.Cols)
	}
}

func TestDropStalePreloadBumpsGeneration(t *testing.T) {
	engine := &playbackFakeEngine{playing: true}
	pl := playlist.New()
	pl.Add(
		playlist.Track{Path: "/a.mp3", Title: "A"},
		playlist.Track{Path: "/b.mp3", Title: "B"},
		playlist.Track{Path: "/c.mp3", Title: "C"},
	)
	m := Model{
		player:     engine,
		playlist:   pl,
		preloading: true,
		preloadFor: "/stale.mp3",
	}
	before := m.requests.preload
	m.dropStalePreload()
	if m.preloading || m.preloadFor != "" {
		t.Fatalf("preloading=%t preloadFor=%q, want cleared", m.preloading, m.preloadFor)
	}
	if m.requests.preload == before {
		t.Fatal("dropStalePreload did not increment requests.preload")
	}
}

func TestPlMgrTracksScrollLargeListLinear(t *testing.T) {
	tracks := make([]playlist.Track, 5000)
	for i := range tracks {
		tracks[i] = playlist.Track{Path: "/a.mp3", Title: "Track", Album: "Album"}
	}
	m := Model{}
	m.plManager.showAlbumHeaders = true
	m.plManager.tracks = tracks
	m.plManager.cursor = len(tracks) - 1
	m.plManager.scroll = 0

	start := time.Now()
	m.plMgrTracksMaybeAdjustScroll(12)
	if elapsed := time.Since(start); elapsed > 50*time.Millisecond {
		t.Fatalf("scroll took %v on 5000 tracks", elapsed)
	}
	if m.plManager.scroll < len(tracks)-12 {
		t.Fatalf("scroll = %d, want >= %d", m.plManager.scroll, len(tracks)-12)
	}
}

func TestHeadlessModeSkipsSpinnerAndPausedDecay(t *testing.T) {
	engine := &playbackFakeEngine{playing: true, paused: true}
	m := Model{
		player:   engine,
		playlist: playlist.New(),
		vis:      ui.NewVisualizer(44100),
	}
	m.SetHeadless(true)
	m.feedLoading = true
	if m.spinnerVisible() {
		t.Fatal("spinnerVisible returned true in headless mode")
	}
	if m.visualizerSettlingPaused() {
		t.Fatal("visualizerSettlingPaused returned true in headless mode")
	}
}

func TestChatPickerEmptyUpDoesNotGoNegative(t *testing.T) {
	m := Model{}
	m.provPane.picking = true
	m.handleChatPickerKey(tea.KeyPressMsg{Code: tea.KeyUp})
	if m.provPane.cursor < 0 {
		t.Fatalf("cursor = %d, want >= 0", m.provPane.cursor)
	}
}
