package model

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/bjarneo/cliamp/favorites"
	"github.com/bjarneo/cliamp/history"
	"github.com/bjarneo/cliamp/luaplugin"
	"github.com/bjarneo/cliamp/playlist"
	"github.com/bjarneo/cliamp/provider"
)

type stubProgressProvider struct {
	name      string
	playlists []playlist.PlaylistInfo
	reports   []progressReportCall
}

type progressReportCall struct {
	path string
	pos  time.Duration
}

func (s *stubProgressProvider) Name() string { return s.name }
func (s *stubProgressProvider) Playlists() ([]playlist.PlaylistInfo, error) {
	return s.playlists, nil
}
func (s *stubProgressProvider) Tracks(string) ([]playlist.Track, error) { return nil, nil }
func (s *stubProgressProvider) CanReportPlayback(playlist.Track) bool   { return true }
func (s *stubProgressProvider) ReportNowPlaying(playlist.Track) error   { return nil }
func (s *stubProgressProvider) ReportScrobble(playlist.Track, time.Time) error {
	return nil
}
func (s *stubProgressProvider) ReportProgress(t playlist.Track, pos time.Duration) error {
	s.reports = append(s.reports, progressReportCall{path: t.Path, pos: pos})
	return nil
}

type stubSearchProvider struct {
	name string
}

func (s *stubSearchProvider) Name() string                                { return s.name }
func (s *stubSearchProvider) Playlists() ([]playlist.PlaylistInfo, error) { return nil, nil }
func (s *stubSearchProvider) Tracks(string) ([]playlist.Track, error)     { return nil, nil }
func (s *stubSearchProvider) SearchTracks(context.Context, string, int) ([]playlist.Track, error) {
	return nil, nil
}

func TestNavTracksLoadedClearsStaleFilterIndices(t *testing.T) {
	m := Model{
		playlist: playlist.New(),
		width:    100,
		height:   30,
	}
	m.layout.panelWidth = 80
	m.navBrowser = navBrowserState{
		visible:   true,
		screen:    navBrowseScreenList,
		artists:   []provider.ArtistInfo{{ID: "1", Name: "A"}, {ID: "2", Name: "B"}, {ID: "3", Name: "Match"}},
		search:    "match",
		searching: true,
		searchIdx: []int{2},
	}
	m.requests.nav = 1

	m.handleNavTracksLoaded(navTracksLoadedMsg{
		gen:    1,
		tracks: []playlist.Track{{Path: "t1.mp3", Title: "Only Track"}},
	})

	if m.navBrowser.search != "" || m.navBrowser.searching || len(m.navBrowser.searchIdx) != 0 {
		t.Fatalf("search state not cleared on tracks load: search=%q searching=%v idx=%v",
			m.navBrowser.search, m.navBrowser.searching, m.navBrowser.searchIdx)
	}
	// Rendering and navDisplayedTracks must not panic.
	_ = m.renderNavTrackBody(10)
	if got := m.navDisplayedTracks(); len(got) != 1 {
		t.Fatalf("navDisplayedTracks() len = %d, want 1", len(got))
	}
}

func TestNavArtistsAndGenresLoadedRecomputeActiveFilter(t *testing.T) {
	m := Model{playlist: playlist.New()}
	m.requests.nav = 1
	m.navBrowser = navBrowserState{
		visible:   true,
		mode:      navBrowseModeByArtist,
		screen:    navBrowseScreenList,
		search:    "miles",
		searching: true,
	}
	m.handleNavArtistsLoaded(navArtistsLoadedMsg{
		gen:     1,
		artists: []provider.ArtistInfo{{ID: "1", Name: "Coltrane"}, {ID: "2", Name: "Miles Davis"}},
	})
	if len(m.navBrowser.searchIdx) != 1 || m.navBrowser.searchIdx[0] != 1 {
		t.Fatalf("artists searchIdx = %v, want [1]", m.navBrowser.searchIdx)
	}

	m.navBrowser.mode = navBrowseModeByGenre
	m.navBrowser.search = "jazz"
	m.navBrowser.searchIdx = nil
	m.handleNavGenresLoaded(navGenresLoadedMsg{
		gen:    1,
		genres: []provider.GenreInfo{{Name: "Rock"}, {Name: "Jazz"}},
	})
	if len(m.navBrowser.searchIdx) != 1 || m.navBrowser.searchIdx[0] != 1 {
		t.Fatalf("genres searchIdx = %v, want [1]", m.navBrowser.searchIdx)
	}
}

func TestReplaceProviderListsRecomputesActiveSearch(t *testing.T) {
	prov := &stubProgressProvider{name: "Test"}
	m := Model{
		provider: prov,
		playlist: playlist.New(),
		width:    100,
		height:   30,
	}
	m.layout.panelWidth = 80
	m.provPane.setLists([]playlist.PlaylistInfo{
		{ID: "1", Name: "Alpha"},
		{ID: "2", Name: "Beta"},
		{ID: "3", Name: "Gamma"},
	})
	m.provSearch = provSearchState{
		active:  true,
		query:   "gamma",
		results: []int{2},
	}

	// Replace with shorter list; stale index 2 must not remain or panic on render.
	m.replaceProviderLists([]playlist.PlaylistInfo{{ID: "1", Name: "Alpha"}})
	if len(m.provSearch.results) != 0 {
		t.Fatalf("provSearch.results = %v, want empty", m.provSearch.results)
	}
	_ = m.renderProviderList()
}

func TestTickProgressReportSkipsBufferingAndRunsAfterGapless(t *testing.T) {
	prov := &stubProgressProvider{name: "Sub"}
	eng := &playbackFakeEngine{playing: true, position: 42 * time.Second, duration: 120 * time.Second}
	p := playlist.New()
	p.Replace([]playlist.Track{
		{Path: "a.mp3", Title: "A", DurationSecs: 120},
		{Path: "b.mp3", Title: "B", DurationSecs: 120},
	})
	p.SetIndex(0)
	m := Model{
		player:    eng,
		playlist:  p,
		providers: []provider.Entry{{Key: "sub", Provider: prov}},
		buffering: true,
	}
	startedPlaybackTrack(&m, p.Tracks()[0])

	now := time.Now()
	m.tickProgressReport(now)
	if len(prov.reports) != 0 {
		t.Fatalf("tickProgressReport reported while buffering: %+v", prov.reports)
	}

	// Simulate gapless transition on tick: player position is now 0 for track B,
	// while currentPlaybackTrack was still track A before tickGapless runs.
	m.buffering = false
	eng.position = 0
	eng.gaplessAdvanced = true
	m.handleTick(tickMsg(now))
	m.WaitReports(time.Second)
	for _, r := range prov.reports {
		if r.path == "a.mp3" && r.pos == 0 {
			t.Fatalf("tickProgressReport reported position 0 for finished track A during gapless swap")
		}
	}
}

func TestSearchOverlayQueryEditCancelsInFlightLoading(t *testing.T) {
	prov := &stubSearchProvider{name: "Mock"}
	canceled := false
	m := Model{
		playlist: playlist.New(),
		searchOverlay: searchOverlayState{
			prov:    prov,
			visible: true,
			screen:  searchOverlayInput,
			query:   "beatl",
			request: "beatl",
			loading: true,
			cancel:  func() { canceled = true },
		},
	}
	m.requests.searchOverlay = 1

	m.handleSearchOverlayInputKey(tea.KeyPressMsg{Code: 'e', Text: "e"})
	if !canceled {
		t.Fatal("editing search query did not cancel in-flight search request")
	}
	if m.searchOverlay.loading {
		t.Fatal("editing search query left searchOverlay.loading stuck true")
	}

	// Stale result for the old query must be ignored cleanly.
	m.handleSearchOverlayResults(searchOverlayResultsMsg{
		gen:          1,
		providerName: "Mock",
		query:        "beatl",
		tracks:       []playlist.Track{{Path: "old.mp3", Title: "Old"}},
	})
	if m.searchOverlay.screen != searchOverlayInput {
		t.Fatalf("stale search result changed screen to %v", m.searchOverlay.screen)
	}
}

func TestBeginPlaybackTrackClearsLyricsLoadingOnEmptyMetadata(t *testing.T) {
	eng := &playbackFakeEngine{}
	p := playlist.New()
	m := Model{
		player:   eng,
		playlist: p,
		lyrics:   lyricsState{visible: true, loading: true},
	}

	m.beginPlaybackTrack(playlist.Track{Path: "https://radio.example.com/stream", Stream: true})
	if m.lyrics.loading {
		t.Fatal("lyrics.loading remained true when switching to track with empty metadata")
	}
}

func TestYTDLUnpauseReconnectSupersededBySeekDoesNotUnpause(t *testing.T) {
	eng := &playbackFakeEngine{playing: true, paused: true, ytdlSeek: true, position: 10 * time.Second, duration: 120 * time.Second}
	p := playlist.New()
	p.Replace([]playlist.Track{{Path: "https://youtube.com/watch?v=abc", Title: "YT", Stream: true, DurationSecs: 120}})
	p.SetIndex(0)
	m := Model{
		player:   eng,
		playlist: p,
	}
	startedPlaybackTrack(&m, p.Tracks()[0])

	cmd := m.reconnectYTDLOnUnpause()
	if cmd == nil {
		t.Fatal("reconnectYTDLOnUnpause returned nil cmd")
	}
	// User seeks before the reconnect completes.
	m.queueSeekTarget(30*time.Second, seekDebounceTicks)

	// First reconnect finishes after being cancelled.
	msg := cmd().(ytdlUnpauseReconnectMsg)
	m.handleYTDLUnpauseReconnect(msg)

	if !eng.paused {
		t.Fatal("canceled reconnect unpaused player after newer seek")
	}
	if !m.seek.active || m.seek.timer == 0 {
		t.Fatalf("canceled reconnect cleared newer seek state: active=%v timer=%d", m.seek.active, m.seek.timer)
	}
}

func TestFileBrowserWOnParentEntryDoesNotSelectParent(t *testing.T) {
	m := Model{
		playlist: playlist.New(),
		fileBrowser: fileBrowserState{
			visible:  true,
			dir:      "/tmp/music",
			entries:  []fbEntry{{name: "..", path: "/tmp", isDir: true, isParent: true}},
			selected: make(map[string]bool),
		},
	}
	cmd := m.handleFileBrowserKey(tea.KeyPressMsg{Code: 'w', Text: "w"})
	if cmd != nil || len(m.fileBrowser.selected) != 0 {
		t.Fatalf("pressing w on .. selected parent: cmd=%v selected=%v", cmd, m.fileBrowser.selected)
	}
}

func TestInPlaceSeekClearsPreloadGenerationWithoutPoisoningPreloadFailed(t *testing.T) {
	m, eng := armedModel()
	m.preloading = true
	m.requests.preload = 3
	eng.seekable = true

	m.seekRelative(5*time.Second, 0)
	if m.requests.preload == 3 {
		t.Fatal("in-place seek did not bump preload request generation")
	}

	// Late errSourceSuperseded from the canceled preload must not poison preloadFailed.
	updated, _ := m.Update(streamPreloadedMsg{path: "b.mp3", gen: 3, err: errors.New("superseded")})
	m = updated.(Model)
	if m.preloadFailed != "" {
		t.Fatalf("preloadFailed = %q after superseded preload, want empty", m.preloadFailed)
	}
}

func TestPrevTrackDuringBufferingMovesToPreviousTrack(t *testing.T) {
	eng := &playbackFakeEngine{playing: true, position: 45 * time.Second, duration: 180 * time.Second, seekable: true}
	p := playlist.New()
	p.Replace([]playlist.Track{
		{Path: "a.mp3", Title: "A", DurationSecs: 180},
		{Path: "b.mp3", Title: "B", DurationSecs: 180},
	})
	p.SetIndex(1)
	m := Model{
		player:    eng,
		playlist:  p,
		buffering: true,
	}
	m.setPlaybackTrack(p.Tracks()[1])

	_ = m.prevTrack()
	if got := m.playlist.Index(); got != 0 {
		t.Fatalf("prevTrack() during buffering left index = %d, want 0", got)
	}
}

func TestNavTrackAPlaysAppendedTrackWhenStoppedAndSkipsStartWhenBuffering(t *testing.T) {
	eng := &playbackFakeEngine{playing: false}
	p := playlist.New()
	p.Replace([]playlist.Track{{Path: "old.mp3", Title: "Old"}})
	p.SetIndex(0)
	m := Model{
		player:   eng,
		playlist: p,
		navBrowser: navBrowserState{
			visible: true,
			screen:  navBrowseScreenTracks,
			tracks:  []playlist.Track{{Path: "new.mp3", Title: "New"}},
		},
	}

	_ = m.handleNavTrackListKey(tea.KeyPressMsg{Code: 'a', Text: "a"})
	if got := m.playlist.Index(); got != 1 {
		t.Fatalf("index after nav a when stopped = %d, want 1", got)
	}

	// When buffering, nav a must not interrupt the buffering track.
	eng.playing = false
	m.buffering = true
	cmd := m.handleNavTrackListKey(tea.KeyPressMsg{Code: 'a', Text: "a"})
	if cmd != nil {
		t.Fatal("nav a started playback while another track was buffering")
	}
}

func TestGenerationZeroGuardsRejectStaleMessages(t *testing.T) {
	eng := &playbackFakeEngine{}
	p := playlist.New()
	m := Model{
		player:      eng,
		playlist:    p,
		feedLoading: true,
		feedRequest: 1,
	}
	m.requests.stream = 1

	cmd := m.handleSubsEpisodes(subsEpisodesMsg{
		mode:      subsLoadPlay,
		streamGen: 0,
		tracks:    []playlist.Track{{Path: "stale.mp3", Title: "Stale"}},
	})
	if cmd != nil || m.playlist.Len() != 0 {
		t.Fatalf("stale subsEpisodesMsg with streamGen=0 hijacked playback")
	}

	m.handleFeedsLoaded(feedsLoadedMsg{
		gen:    0,
		tracks: []playlist.Track{{Path: "stale-feed.mp3", Title: "Stale"}},
	})
	if !m.feedLoading || m.playlist.Len() != 0 {
		t.Fatalf("stale feedsLoadedMsg with gen=0 cleared feedLoading or appended tracks")
	}
}

func TestPlMgrRenameAndPickerChooseRenderErrors(t *testing.T) {
	m := Model{
		playlist: playlist.New(),
		width:    80,
		height:   24,
		plManager: plManagerState{
			visible:  true,
			screen:   plMgrScreenRename,
			inputErr: "Name already exists",
		},
		plPicker: playlistPickerState{
			visible:   true,
			screen:    plPickerChoose,
			playlists: []playlist.PlaylistInfo{{ID: "rock", Name: "Rock"}},
			tracks:    []playlist.Track{{Path: "a.mp3", Title: "A"}},
			inputErr:  "Write failed: permission denied",
		},
	}
	m.layout.panelWidth = 76
	m.plVisible = 6

	if got := m.renderPlMgrFormBody(); !strings.Contains(got, "Name already exists") {
		t.Fatalf("renderPlMgrFormBody() missing rename error: %q", got)
	}
	if got := m.renderPlaylistPickerBody(); !strings.Contains(got, "Write failed: permission denied") {
		t.Fatalf("renderPlaylistPickerBody() missing choose-screen error: %q", got)
	}
}

func TestPlMgrListScrollAccountsForBlankSeparators(t *testing.T) {
	m := Model{
		playlist: playlist.New(),
		plManager: plManagerState{
			visible: true,
			screen:  plMgrScreenList,
			playlists: []playlist.PlaylistInfo{
				{Name: history.PlaylistName},
				{Name: "P1"},
				{Name: "P2"},
				{Name: "P3"},
				{Name: "P4"},
			},
			cursor: 5, // "+ New Playlist..."
		},
	}
	m.layout.panelWidth = 60
	m.plVisible = 5
	m.plMgrListMaybeAdjustScroll(5)
	rendered := m.renderPlMgrListBody()
	if !strings.Contains(rendered, "+ New Playlist...") {
		t.Fatalf("+ New Playlist... scrolled out of view: scroll=%d rendered=%q", m.plManager.scroll, rendered)
	}
}

func TestSwitchProviderClearsAuthURLAndReplaceDisarmsReconnect(t *testing.T) {
	prov1 := &stubProgressProvider{name: "One"}
	prov2 := &stubProgressProvider{name: "Two"}
	eng := &playbackFakeEngine{}
	p := playlist.New()
	m := Model{
		player:   eng,
		playlist: p,
		providers: []provider.Entry{
			{Key: "one", Provider: prov1},
			{Key: "two", Provider: prov2},
		},
		provider: prov1,
	}
	m.provPane.authURL = "https://oauth.example.com/start"
	m.reconnect.at = time.Now().Add(time.Second)

	m.switchProvider(1)
	if m.provPane.authURL != "" {
		t.Fatalf("switchProvider left stale authURL = %q", m.provPane.authURL)
	}

	m.replacePlayerPlaylist([]playlist.Track{{Path: "x.mp3", Title: "X"}})
	if !m.reconnect.at.IsZero() {
		t.Fatal("replacePlayerPlaylist left pending reconnect armed")
	}
}

func TestLuaSelectSpaceConfirmsOption(t *testing.T) {
	gotIdx := -99
	m := Model{
		luaSelect: &luaSelectState{
			title:   "Pick",
			options: []string{"One", "Two"},
			cursor:  1,
			onDone:  func(idx int) { gotIdx = idx },
		},
	}
	m.handleLuaSelectKey(tea.KeyPressMsg{Code: tea.KeySpace, Text: " "})
	if gotIdx != 1 {
		t.Fatalf("Space in Lua select modal got idx=%d, want 1", gotIdx)
	}
}

func TestClampPositionNonNegativeForShortTracks(t *testing.T) {
	m := Model{player: &playbackFakeEngine{duration: 500 * time.Millisecond}}
	if got := m.clampPosition(time.Second); got < 0 {
		t.Fatalf("clampPosition(1s) on 500ms track = %v, want >= 0", got)
	}
}

func TestTickDrainEmitsQueueEndWithLiveDuration(t *testing.T) {
	eng := &playbackFakeEngine{playing: true, drained: true, duration: 95 * time.Second}
	p := playlist.New()
	p.Replace([]playlist.Track{{Path: "last.mp3", Title: "Last"}})
	p.SetIndex(0)
	mgr, messages, closePlugins := newReportTestPlugin(t, luaplugin.EventQueueEnd, `tostring(ev.duration)`)
	defer closePlugins()

	m := Model{
		player:   eng,
		playlist: p,
		luaMgr:   mgr,
	}
	startedPlaybackTrack(&m, p.Tracks()[0])

	m.tickDrain(time.Now())
	closePlugins()
	select {
	case msg := <-messages:
		if msg != "95" {
			t.Fatalf("EventQueueEnd duration = %q, want 95", msg)
		}
	case <-time.After(time.Second):
		t.Fatal("EventQueueEnd was not emitted")
	}
}

func TestIsFullyIdleFalseDuringSeekAndPendingConfigSaves(t *testing.T) {
	base := func() Model {
		return Model{player: &playbackFakeEngine{playing: true, paused: true}}
	}
	if m := base(); !m.isFullyIdle() {
		t.Fatal("baseline paused model should be idle")
	}
	m := base()
	m.seek.active = true
	if m.isFullyIdle() {
		t.Fatal("isFullyIdle() = true while seek.active")
	}
	m = base()
	m.speedSaveAfter = time.Second
	if m.isFullyIdle() {
		t.Fatal("isFullyIdle() = true while speedSaveAfter > 0")
	}
	m = base()
	m.eqSaveAfter = time.Second
	if m.isFullyIdle() {
		t.Fatal("isFullyIdle() = true while eqSaveAfter > 0")
	}
}

func TestOneRowBodyBudgetsDoNotOverflow(t *testing.T) {
	m := Model{
		playlist: playlist.New(),
		plPicker: playlistPickerState{
			visible:   true,
			screen:    plPickerChoose,
			playlists: []playlist.PlaylistInfo{{ID: "a", Name: "A"}},
			tracks:    []playlist.Track{{Path: "a.mp3", Title: "A"}},
		},
		searchOverlay: searchOverlayState{
			visible:   true,
			screen:    searchOverlayPlaylist,
			playlists: []playlist.PlaylistInfo{{ID: "a", Name: "A"}},
			selTrack:  playlist.Track{Artist: "Art", Title: "Title"},
			err:       "boom",
		},
	}
	m.layout.panelWidth = 60
	m.plVisible = 1

	if lines := strings.Split(m.renderPlaylistPickerBody(), "\n"); len(lines) > 1 {
		t.Fatalf("renderPlaylistPickerBody() lines = %d with budget 1, want <= 1", len(lines))
	}
	if lines := strings.Split(m.renderSearchOverlayBody(), "\n"); len(lines) > 1 {
		t.Fatalf("renderSearchOverlayBody() lines = %d with budget 1, want <= 1", len(lines))
	}
}

func TestVirtualPlaylistBlocksFileBrowserOAndEmptyFBConfirmIsNoop(t *testing.T) {
	m := Model{
		playlist: playlist.New(),
		plManager: plManagerState{
			visible:     true,
			screen:      plMgrScreenTracks,
			selPlaylist: favorites.PlaylistName,
		},
	}
	m.handlePlMgrTracksKey(tea.KeyPressMsg{Code: 'o', Text: "o"})
	if m.fileBrowser.visible {
		t.Fatal("pressing o on virtual playlist opened file browser")
	}

	m.fileBrowser = fileBrowserState{
		visible:        true,
		targetPlaylist: favorites.PlaylistName,
		selected:       map[string]bool{},
	}
	if cmd := m.fbConfirm(false); cmd != nil {
		t.Fatal("fbConfirm with empty selection returned non-nil command")
	}
}
