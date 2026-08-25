package model

import (
	"fmt"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/bjarneo/cliamp/playlist"
	"github.com/bjarneo/cliamp/theme"
	"github.com/bjarneo/cliamp/ui"
)

func newLayoutTestModel(width, height int) Model {
	player := &playbackFakeEngine{}
	pl := playlist.New()
	for i := range 16 {
		pl.Add(playlist.Track{
			Path:  fmt.Sprintf("/tmp/track-%d.mp3", i),
			Title: "A very long 音楽 track title that must remain inside the terminal",
		})
	}
	m := Model{
		player:   player,
		playlist: pl,
		vis:      ui.NewVisualizer(float64(player.SampleRate())),
		width:    width,
		height:   height,
		focus:    focusPlaylist,
	}
	m.vis.Mode = ui.VisBars
	m.recomputeLayout()
	return m
}

func TestFrameLayoutTiers(t *testing.T) {
	tests := []struct {
		name        string
		width       int
		height      int
		wantTier    layoutTier
		wantVisRows int
	}{
		{name: "too small", width: 39, height: 9, wantTier: layoutTooSmall},
		{name: "minimal", width: 40, height: 10, wantTier: layoutMinimal},
		{name: "compact", width: 56, height: 16, wantTier: layoutCompact, wantVisRows: 3},
		{name: "full", width: 80, height: 24, wantTier: layoutFull, wantVisRows: ui.DefaultVisRows},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := newLayoutTestModel(tt.width, tt.height)
			if m.layout.tier != tt.wantTier {
				t.Fatalf("layout tier = %v, want %v", m.layout.tier, tt.wantTier)
			}
			if tt.wantTier == layoutTooSmall {
				if m.layout.bodyRows != 0 {
					t.Fatalf("body rows = %d, want 0", m.layout.bodyRows)
				}
				return
			}
			if m.layout.bodyRows < 1 {
				t.Fatalf("body rows = %d, want at least one", m.layout.bodyRows)
			}
			if m.vis.Rows != tt.wantVisRows {
				t.Fatalf("visualizer rows = %d, want %d", m.vis.Rows, tt.wantVisRows)
			}
			if m.vis.Cols != m.layout.panelWidth {
				t.Fatalf("visualizer columns = %d, want %d", m.vis.Cols, m.layout.panelWidth)
			}
		})
	}
}

func TestResponsiveViewsFitTerminal(t *testing.T) {
	for _, size := range []struct{ width, height int }{
		{39, 9},
		{40, 10},
		{56, 16},
		{80, 20},
		{80, 24},
		{120, 40},
	} {
		t.Run(fmt.Sprintf("%dx%d", size.width, size.height), func(t *testing.T) {
			m := newLayoutTestModel(size.width, size.height)
			m.status.text = "a status message\nthat must not create another row"
			out := m.View().Content
			if got := lipgloss.Height(out); got > size.height {
				t.Fatalf("view height = %d, want <= %d\n%s", got, size.height, out)
			}
			for _, line := range strings.Split(out, "\n") {
				if got := lipgloss.Width(line); got > size.width {
					t.Fatalf("line width = %d, want <= %d: %q", got, size.width, line)
				}
			}
		})
	}
}

func TestExpandedPlaylistUsesAvailableRows(t *testing.T) {
	m := newLayoutTestModel(80, 50)
	if m.plVisible != maxPlVisible {
		t.Fatalf("collapsed playlist rows = %d, want %d", m.plVisible, maxPlVisible)
	}

	m.heightExpanded = true
	m.recomputeLayout()
	if m.plVisible != m.layout.bodyRows {
		t.Fatalf("expanded playlist rows = %d, want available body rows %d", m.plVisible, m.layout.bodyRows)
	}
	if m.plVisible <= maxPlExpandVisible {
		t.Fatalf("expanded playlist rows = %d, want more than previous cap %d", m.plVisible, maxPlExpandVisible)
	}
}

func TestCollapsedPlaylistCentersFrameVertically(t *testing.T) {
	m := newLayoutTestModel(80, 50)
	body := ui.FitRect(m.renderMainBody(), m.layout.panelWidth, m.layout.bodyRows)
	content := strings.Join(m.mainSections(body, true, false), "\n")
	frameHeight := lipgloss.Height(ui.FrameStyle.Render(content))
	wantTopPadding := (m.height - frameHeight) / 2

	out := m.View().Content
	gotTopPadding := 0
	for _, line := range strings.Split(out, "\n") {
		if line != "" {
			break
		}
		gotTopPadding++
	}
	if gotTopPadding != wantTopPadding {
		t.Fatalf("top padding = %d, want %d", gotTopPadding, wantTopPadding)
	}
}

func TestResizeClampsActiveOverlayCursor(t *testing.T) {
	m := newLayoutTestModel(120, 40)
	m.themePicker.visible = true
	m.themes = make([]theme.Theme, 40)
	m.themePicker.cursor = 40
	m.themePicker.scroll = 35

	updated, _ := m.Update(tea.WindowSizeMsg{Width: 56, Height: 16})
	m = updated.(Model)
	if m.themePicker.cursor >= len(m.themes)+1 {
		t.Fatalf("theme cursor = %d, want within %d entries", m.themePicker.cursor, len(m.themes)+1)
	}
	if m.themePicker.cursor < m.themePicker.scroll || m.themePicker.cursor >= m.themePicker.scroll+m.themePickerVisible() {
		t.Fatalf("theme cursor %d outside viewport [%d,%d)", m.themePicker.cursor, m.themePicker.scroll, m.themePicker.scroll+m.themePickerVisible())
	}
}

func TestContentFirstLayoutPrioritizesLists(t *testing.T) {
	playback := newLayoutTestModel(80, 24)
	browse := newLayoutTestModel(80, 24)
	browse.keymap.visible = true
	browse.keymap.entries = browse.buildKeymapEntries()
	browse.recomputeLayout()

	if !browse.usesContentFirstLayout() {
		t.Fatal("keymap must use the content-first layout")
	}
	if browse.layout.bodyRows <= playback.layout.bodyRows {
		t.Fatalf("content-first body rows = %d, want more than playback rows %d", browse.layout.bodyRows, playback.layout.bodyRows)
	}
	if browse.layout.visualizerRows != 0 {
		t.Fatalf("content-first visualizer rows = %d, want 0", browse.layout.visualizerRows)
	}
	if browse.vis.Rows < 1 {
		t.Fatalf("content-first visualizer canvas rows = %d, want positive", browse.vis.Rows)
	}

	preview := newLayoutTestModel(80, 24)
	preview.visPicker.visible = true
	preview.visPicker.modes = preview.vis.AllModeNames()
	preview.recomputeLayout()
	if preview.usesContentFirstLayout() {
		t.Fatal("visualizer picker must retain the live preview layout")
	}
	if preview.layout.visualizerRows == 0 {
		t.Fatal("visualizer picker must retain visualizer rows")
	}
}

func TestResizeHidesMinimalVisualizerAndRestoresCanvas(t *testing.T) {
	m := newLayoutTestModel(80, 24)
	p := m.player.(*playbackFakeEngine)
	p.playing = true
	m.vis.Mode = ui.VisScope
	t0 := time.Unix(1, 0)
	m.tickVisualizer(t0)
	before := m.vis.Frame()

	updated, _ := m.Update(tea.WindowSizeMsg{Width: 40, Height: 10})
	m = updated.(Model)
	if m.visualizerVisible() {
		t.Fatal("visualizerVisible() = true in minimal layout, want false")
	}
	if m.vis.Rows != 0 {
		t.Fatalf("minimal visualizer rows = %d, want 0", m.vis.Rows)
	}
	m.tickVisualizer(t0.Add(time.Second))
	if got := m.vis.Frame(); got != before {
		t.Fatalf("hidden visualizer frame = %d, want %d", got, before)
	}

	updated, _ = m.Update(tea.WindowSizeMsg{Width: 56, Height: 16})
	m = updated.(Model)
	if !m.visualizerVisible() {
		t.Fatal("visualizerVisible() = false after compact resize, want true")
	}
	if m.vis.Rows != 3 {
		t.Fatalf("restored visualizer rows = %d, want 3", m.vis.Rows)
	}
	m.tickVisualizer(t0.Add(2 * time.Second))
	if got := m.vis.Frame(); got != before+1 {
		t.Fatalf("restored visualizer frame = %d, want %d without hidden-time catch-up", got, before+1)
	}
}

func TestMinimalLayoutRejectsHiddenMainFocus(t *testing.T) {
	m := newLayoutTestModel(80, 24)
	m.focus = focusEQ
	m.prevFocus = focusProvPill

	updated, _ := m.Update(tea.WindowSizeMsg{Width: 40, Height: 10})
	m = updated.(Model)
	if m.focus != focusPlaylist {
		t.Fatalf("focus = %v, want playlist at minimal size", m.focus)
	}
	if m.prevFocus != focusPlaylist {
		t.Fatalf("previous focus = %v, want playlist at minimal size", m.prevFocus)
	}

	m.handleKey(tea.KeyPressMsg{Code: tea.KeyTab})
	if m.focus != focusPlaylist {
		t.Fatalf("focus after Tab = %v, want playlist at minimal size", m.focus)
	}
	beforePreset := m.eqPresetIdx
	m.handleKey(tea.KeyPressMsg{Text: "e"})
	if m.eqPresetIdx != beforePreset {
		t.Fatalf("EQ preset = %d after hidden shortcut, want %d", m.eqPresetIdx, beforePreset)
	}
}

func TestCompactEqualizerShowsActiveBand(t *testing.T) {
	m := newLayoutTestModel(56, 16)
	m.focus = focusEQ
	m.eqCursor = 4

	if plain := lipgloss.NewStyle().Render(m.renderCompactControls()); !strings.Contains(plain, "1k") {
		t.Fatalf("compact controls = %q, want active EQ band", plain)
	}
}

func TestSimplifiedLayoutShowsTrackSummaryAndTimeStrip(t *testing.T) {
	m := newLayoutTestModel(80, 40)
	track, ok := m.playlist.Track(0)
	if !ok {
		t.Fatal("playlist has no first track")
	}
	track.Artist = "Artist"
	track.Title = "Title"
	track.DurationSecs = 222
	m.playlist.SetTrack(0, track)
	m.cachedPos = 61 * time.Second
	m.cachedDur = 222 * time.Second
	m.SetSimplified(true)

	if m.vis.Rows != 0 {
		t.Fatalf("simplified visualizer rows = %d, want 0", m.vis.Rows)
	}
	if m.visualizerVisible() {
		t.Fatal("visualizerVisible() = true in simplified mode, want false")
	}
	if m.plVisible != 0 {
		t.Fatalf("simplified playlist rows = %d, want 0", m.plVisible)
	}
	if m.layout.fixedRows != 3 {
		t.Fatalf("simplified fixed rows = %d, want track, time, and seek", m.layout.fixedRows)
	}

	plain := stripAnsi(m.View().Content)
	assertViewFits(t, plain, 80, 40)
	if !strings.Contains(plain, "Artist - Title") || !strings.Contains(plain, "01:01 / 03:42") {
		t.Fatalf("simplified view = %q, want artist, title, and playback time", plain)
	}
	if strings.Contains(plain, "C L I A M P") || strings.Contains(plain, "EQ ") || strings.Contains(plain, "Playlist") {
		t.Fatalf("simplified view = %q, contains full playback chrome", plain)
	}
}

func TestSimplifiedLayoutDisablesHiddenPlaybackChrome(t *testing.T) {
	m := newLayoutTestModel(80, 24)
	m.focus = focusEQ
	m.fullVis = true
	m.SetSimplified(true)

	if m.focus != focusPlaylist {
		t.Fatalf("focus = %v, want playlist", m.focus)
	}
	if m.fullVis {
		t.Fatal("SetSimplified did not close the full visualizer")
	}
	m.handleKey(tea.KeyPressMsg{Code: tea.KeyTab})
	if m.focus != focusPlaylist {
		t.Fatalf("focus after Tab = %v, want playlist", m.focus)
	}
	beforeMode := m.vis.Mode
	m.handleKey(tea.KeyPressMsg{Text: "v"})
	if m.vis.Mode != beforeMode {
		t.Fatalf("visualizer mode = %v, want %v", m.vis.Mode, beforeMode)
	}
	m.handleKey(tea.KeyPressMsg{Text: "V"})
	if m.fullVis {
		t.Fatal("full visualizer opened in simplified mode")
	}
	m.handleKey(tea.KeyPressMsg{Mod: tea.ModCtrl, Code: 'x'})
	if m.heightExpanded {
		t.Fatal("playlist expanded in simplified mode")
	}
}

func TestAsyncSearchResultLayoutUsesContentFirstRows(t *testing.T) {
	m := newLayoutTestModel(80, 24)
	m.netSearch.active = true
	m.netSearch.screen = netSearchInput
	m.netSearch.request = "ambient"
	m.requests.netSearch = 1
	m.recomputeLayout()
	inputRows := m.layout.bodyRows

	updated, _ := m.Update(netSearchResultsMsg{
		gen:    1,
		query:  "ambient",
		tracks: []playlist.Track{{Title: "Result"}},
	})
	m = updated.(Model)
	if !m.usesContentFirstLayout() {
		t.Fatal("search results must use the content-first layout")
	}
	if m.layout.bodyRows <= inputRows {
		t.Fatalf("result body rows = %d, want more than input rows %d", m.layout.bodyRows, inputRows)
	}
}

func TestLayoutClampsConfiguredPadding(t *testing.T) {
	previousStyle := ui.FrameStyle
	previousPanelWidth := ui.PanelWidth
	previousPaddingH := ui.PaddingH
	previousPaddingV := ui.VerticalPadding()
	ui.SetPadding(10, 5)
	t.Cleanup(func() {
		ui.SetPadding(previousPaddingH, previousPaddingV)
		ui.FrameStyle = previousStyle
		ui.PanelWidth = previousPanelWidth
	})

	m := newLayoutTestModel(40, 10)
	if m.layout.panelWidth <= 0 {
		t.Fatalf("panel width = %d, want positive", m.layout.panelWidth)
	}
	if got := m.View().Content; lipgloss.Height(got) > 10 {
		t.Fatalf("view height = %d, want <= 10", lipgloss.Height(got))
	}
}

func TestViewsFitConfiguredPaddingExtremes(t *testing.T) {
	previousStyle := ui.FrameStyle
	previousPanelWidth := ui.PanelWidth
	previousPaddingH := ui.PaddingH
	previousPaddingV := ui.VerticalPadding()
	t.Cleanup(func() {
		ui.SetPadding(previousPaddingH, previousPaddingV)
		ui.FrameStyle = previousStyle
		ui.PanelWidth = previousPanelWidth
	})

	for _, tt := range []struct {
		name     string
		paddingH int
		paddingV int
	}{
		{name: "zero", paddingH: 0, paddingV: 0},
		{name: "default", paddingH: 2, paddingV: 1},
		{name: "maximum", paddingH: 10, paddingV: 5},
	} {
		t.Run(tt.name, func(t *testing.T) {
			ui.SetPadding(tt.paddingH, tt.paddingV)
			m := newLayoutTestModel(80, 24)
			assertViewFits(t, m.View().Content, 80, 24)
		})
	}
}

func TestLongUnicodeContentFitsTerminal(t *testing.T) {
	for _, size := range []struct{ width, height int }{{40, 10}, {80, 24}, {120, 40}} {
		t.Run(fmt.Sprintf("%dx%d", size.width, size.height), func(t *testing.T) {
			m := newLayoutTestModel(size.width, size.height)
			track := m.playlist.Tracks()[0]
			track.Title = strings.Repeat("界e\u0301", 48)
			track.Album = strings.Repeat("https://provider.example/playlist/", 8)
			m.playlist.SetTrack(0, track)
			m.providers = []ProviderEntry{
				{Name: strings.Repeat("Very Long Provider ", 8)},
				{Name: "Local"},
			}
			m.status.Warning(strings.Repeat("https://stream.example/very/long/error/", 8), statusTTLDefault)
			assertViewFits(t, m.View().Content, size.width, size.height)
		})
	}
}

func TestTooSmallLayoutBlocksHiddenMutations(t *testing.T) {
	m := newLayoutTestModel(39, 9)
	before := m.playlist.Len()
	m.handleKey(tea.KeyPressMsg{Text: "x"})
	if got := m.playlist.Len(); got != before {
		t.Fatalf("playlist length = %d after hidden remove, want %d", got, before)
	}
}

func TestTrackInfoScrollsWithinBodyBudget(t *testing.T) {
	m := newLayoutTestModel(40, 10)
	track := m.playlist.Tracks()[0]
	track.Artist = "Artist"
	track.Album = "Album"
	track.Genre = "Genre"
	track.Year = 2026
	track.TrackNumber = 1
	m.playlist.SetTrack(0, track)
	m.showInfo = true

	m.handleKey(tea.KeyPressMsg{Text: "j"})
	if m.infoScroll == 0 {
		t.Fatal("info scroll = 0 after down, want a later metadata row")
	}
	if got := m.renderInfoBody(); !strings.Contains(got, "Artist") {
		t.Fatalf("track info body = %q, want scrolled metadata", got)
	}
}

func TestInlineOverlaysFitResponsiveTerminal(t *testing.T) {
	overlays := []struct {
		name string
		set  func(*Model)
	}{
		{name: "keymap", set: func(m *Model) { m.keymap.visible = true; m.keymap.entries = m.buildKeymapEntries() }},
		{name: "theme", set: func(m *Model) { m.themePicker.visible = true }},
		{name: "visualizer", set: func(m *Model) { m.visPicker.visible = true; m.visPicker.modes = m.vis.AllModeNames() }},
		{name: "device", set: func(m *Model) { m.devicePicker.visible = true }},
		{name: "playlist picker", set: func(m *Model) { m.plPicker.visible = true }},
		{name: "file browser", set: func(m *Model) { m.fileBrowser.visible = true }},
		{name: "provider search", set: func(m *Model) { m.spotSearch.visible = true }},
		{name: "navigation", set: func(m *Model) { m.navBrowser.visible = true }},
		{name: "playlist manager", set: func(m *Model) { m.plManager.visible = true }},
		{name: "queue", set: func(m *Model) { m.queue.visible = true }},
		{name: "radio stats", set: func(m *Model) { m.radioStats.visible = true }},
		{name: "info", set: func(m *Model) { m.showInfo = true }},
		{name: "lyrics", set: func(m *Model) { m.lyrics.visible = true }},
		{name: "jump", set: func(m *Model) { m.jumping = true }},
		{name: "url", set: func(m *Model) { m.urlInputting = true }},
		{name: "search", set: func(m *Model) { m.search.active = true }},
		{name: "online search", set: func(m *Model) { m.netSearch.active = true }},
	}

	for _, size := range []struct{ width, height int }{{40, 10}, {56, 16}, {80, 24}} {
		for _, overlay := range overlays {
			t.Run(fmt.Sprintf("%s_%dx%d", overlay.name, size.width, size.height), func(t *testing.T) {
				m := newLayoutTestModel(size.width, size.height)
				overlay.set(&m)
				assertViewFits(t, m.View().Content, size.width, size.height)
			})
		}
	}
}

func assertViewFits(t *testing.T, view string, width, height int) {
	t.Helper()
	if got := lipgloss.Height(view); got > height {
		t.Fatalf("view height = %d, want <= %d\n%s", got, height, view)
	}
	for _, line := range strings.Split(view, "\n") {
		if got := lipgloss.Width(line); got > width {
			t.Fatalf("line width = %d, want <= %d: %q", got, width, line)
		}
	}
}
