package model

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/bjarneo/cliamp/playlist"
	"github.com/bjarneo/cliamp/provider"
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
		{name: "compact", width: 56, height: 16, wantTier: layoutCompact, wantVisRows: compactVisRows},
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
	// Provider count matters: the source row is only drawn with more than one,
	// so a single-provider model never exercises the tallest chrome. Pane
	// state matters for the same reason at the full tier.
	variants := []struct {
		name      string
		providers []provider.Entry
		hidePane  bool
	}{
		{name: "one provider", providers: []provider.Entry{{Name: "Local"}}},
		{name: "many providers", providers: []provider.Entry{{Name: "Local"}, {Name: "Radio"}, {Name: "Navidrome"}}},
		{name: "many providers, pane closed", providers: []provider.Entry{{Name: "Local"}, {Name: "Radio"}}, hidePane: true},
	}

	for _, size := range []struct{ width, height int }{
		{39, 9},
		{40, 10},
		{56, 16},
		{80, 20},
		{80, 24},
		{120, 40},
	} {
		for _, v := range variants {
			t.Run(fmt.Sprintf("%dx%d/%s", size.width, size.height, v.name), func(t *testing.T) {
				m := newLayoutTestModel(size.width, size.height)
				m.providers = v.providers
				m.hideSettings = v.hidePane
				m.recomputeLayout()
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
}

func TestExpandedPlaylistUsesAvailableRows(t *testing.T) {
	m := newLayoutTestModel(80, 50)
	// The two-column body hands the playlist the rows its settings pane freed.
	wantCollapsed := maxPlVisible
	if m.layout.twoColumn {
		wantCollapsed += twoColumnChromeRows
	}
	if m.plVisible != wantCollapsed {
		t.Fatalf("collapsed playlist rows = %d, want %d", m.plVisible, wantCollapsed)
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

func TestExpandedPlaylistWithoutVisualizerFillsTerminal(t *testing.T) {
	for _, size := range []struct {
		width, height int
		wantFixed     int
		extraBodyRows int
	}{
		// extraBodyRows is what turning the visualizer off gives back: its
		// height plus the blank row that framed it at the full tier.
		{width: 80, height: 50, wantFixed: 10, extraBodyRows: ui.DefaultVisRows + 1},
		{width: 80, height: 24, wantFixed: 10, extraBodyRows: ui.DefaultVisRows + 1},
		{width: 56, height: 20, wantFixed: 9, extraBodyRows: compactVisRows},
	} {
		t.Run(fmt.Sprintf("%dx%d", size.width, size.height), func(t *testing.T) {
			mWithVis := newLayoutTestModel(size.width, size.height)
			mWithVis.heightExpanded = true
			mWithVis.recomputeLayout()
			bodyRowsWithVis := mWithVis.layout.bodyRows

			m := newLayoutTestModel(size.width, size.height)
			for i := 16; i < 100; i++ {
				m.playlist.Add(playlist.Track{Path: fmt.Sprintf("/tmp/track-%d.mp3", i), Title: "Track"})
			}
			m.providers = []provider.Entry{{Name: "Local"}, {Name: "Radio"}}
			m.vis.Mode = ui.VisNone
			m.heightExpanded = true
			m.recomputeLayout()

			wantFixed := size.wantFixed
			if m.layout.twoColumn {
				// EQ/volume, source, and the status line moved into the
				// settings pane beside the playlist.
				wantFixed -= twoColumnChromeRows
			}
			if m.layout.fixedRows != wantFixed {
				t.Fatalf("fixed rows = %d, want %d", m.layout.fixedRows, wantFixed)
			}
			if m.layout.bodyRows != bodyRowsWithVis+size.extraBodyRows {
				t.Fatalf("body rows = %d, want %d (%d more than with visualizer)", m.layout.bodyRows, bodyRowsWithVis+size.extraBodyRows, size.extraBodyRows)
			}
			if m.plVisible != m.layout.bodyRows {
				t.Fatalf("expanded playlist rows = %d, want available body rows %d", m.plVisible, m.layout.bodyRows)
			}

			// View without transient status message should not have empty top padding lines
			out := m.View().Content
			lines := strings.Split(out, "\n")
			if lines[0] == "" {
				t.Fatalf("first line is empty, view has extra top padding when expanded without visualizer")
			}

			// When a transient status message is present, view height should match terminal height exactly
			m.status.Show("status", statusTTLDefault)
			outWithStatus := m.View().Content
			if got := lipgloss.Height(outWithStatus); got != size.height {
				t.Fatalf("view height with status = %d, want %d", got, size.height)
			}
		})
	}
}

func TestCollapsedPlaylistCentersFrameVertically(t *testing.T) {
	m := newLayoutTestModel(80, 50)
	body := ui.FitRect(m.renderMainBody(), m.layout.panelWidth, m.layout.bodyRows)
	content := strings.Join(m.mainSections(body, true, false), "\n")
	frameHeight := lipgloss.Height(m.layout.frameStyle().Render(content))
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
	if m.themePicker.cursor < m.themePicker.scroll || m.themePicker.cursor >= m.themePicker.scroll+m.effectivePlaylistVisible() {
		t.Fatalf("theme cursor %d outside viewport [%d,%d)", m.themePicker.cursor, m.themePicker.scroll, m.themePicker.scroll+m.effectivePlaylistVisible())
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
	if m.vis.Rows != compactVisRows {
		t.Fatalf("restored visualizer rows = %d, want %d", m.vis.Rows, compactVisRows)
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

// The frame padding belongs to the Model. A Model that nobody configured
// uses the config defaults.
func TestSetPadding(t *testing.T) {
	tests := []struct {
		name               string
		setup              func(*Model)
		wantH, wantV, cols int
	}{
		{name: "unset", setup: func(*Model) {}, wantH: 3, wantV: 1, cols: 74},
		{name: "zero", setup: func(m *Model) { m.SetPadding(0, 0) }, wantH: 0, wantV: 0, cols: 80},
		{name: "configured", setup: func(m *Model) { m.SetPadding(5, 2) }, wantH: 5, wantV: 2, cols: 70},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := newLayoutTestModel(80, 24)
			tt.setup(&m)
			if m.layout.paddingH != tt.wantH || m.layout.paddingV != tt.wantV {
				t.Fatalf("padding = %d, %d; want %d, %d", m.layout.paddingH, m.layout.paddingV, tt.wantH, tt.wantV)
			}
			if m.vis.Cols != tt.cols {
				t.Fatalf("visualizer columns = %d, want %d", m.vis.Cols, tt.cols)
			}
		})
	}
}

// lifecycleLuaHost records the init and render calls of the Lua visualizers.
type lifecycleLuaHost struct{ calls []string }

func (h *lifecycleLuaHost) RenderVis(name string, _ [ui.DefaultSpectrumBands]float64, rows, cols int, _ uint64) string {
	h.calls = append(h.calls, fmt.Sprintf("render %s %dx%d", name, rows, cols))
	return ""
}

func (h *lifecycleLuaHost) InitVis(name string, rows, cols int) {
	h.calls = append(h.calls, fmt.Sprintf("init %s %dx%d", name, rows, cols))
}

func (h *lifecycleLuaHost) DestroyVis(name string) {
	h.calls = append(h.calls, "destroy "+name)
}

// Bubbletea draws the first frame before the first WindowSizeMsg. A Lua
// visualizer from the config must not get its init with the placeholder
// size of that frame. It gets its init with the real size of the terminal.
func TestLuaVisualizerInitWaitsForWindowSize(t *testing.T) {
	tests := []struct {
		width, height int
		want          []string
	}{
		{200, 50, []string{"init myvis 7x194", "render myvis 7x194"}},
		{100, 30, []string{"init myvis 7x94", "render myvis 7x94"}},
		{60, 18, []string{"init myvis 5x54", "render myvis 5x54"}},
	}
	for _, tt := range tests {
		t.Run(fmt.Sprintf("%dx%d", tt.width, tt.height), func(t *testing.T) {
			host := &lifecycleLuaHost{}
			m := New(&playbackFakeEngine{}, playlist.New(), nil, "", nil, nil, nil, nil, nil, nil)
			m.RegisterLuaVisualizers([]string{"myvis"}, host)
			m.SetVisRows(0)
			if !m.SetVisualizer("myvis") {
				t.Fatal("SetVisualizer(myvis) = false")
			}

			m.View()
			if len(host.calls) != 0 {
				t.Fatalf("calls before the window size = %q, want none", host.calls)
			}

			updated, _ := m.Update(tea.WindowSizeMsg{Width: tt.width, Height: tt.height})
			m = updated.(Model)
			m.View()
			if !slices.Equal(host.calls, tt.want) {
				t.Fatalf("calls = %q, want %q", host.calls, tt.want)
			}
		})
	}
}

func TestLayoutClampsConfiguredPadding(t *testing.T) {
	m := newLayoutTestModel(40, 10)
	m.SetPadding(10, 5)
	if m.layout.panelWidth <= 0 {
		t.Fatalf("panel width = %d, want positive", m.layout.panelWidth)
	}
	if got := m.View().Content; lipgloss.Height(got) > 10 {
		t.Fatalf("view height = %d, want <= 10", lipgloss.Height(got))
	}
}

func TestViewsFitConfiguredPaddingExtremes(t *testing.T) {
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
			m := newLayoutTestModel(80, 24)
			m.SetPadding(tt.paddingH, tt.paddingV)
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
			m.providers = []provider.Entry{
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

// The too-small message wraps to the width, so the required and the current
// size stay in view.
func TestTooSmallMessageWraps(t *testing.T) {
	for _, size := range []struct{ width, height int }{{39, 9}, {30, 8}, {20, 5}, {30, 3}} {
		t.Run(fmt.Sprintf("%dx%d", size.width, size.height), func(t *testing.T) {
			m := newLayoutTestModel(size.width, size.height)
			out := m.View().Content
			lines := strings.Split(out, "\n")
			if len(lines) > size.height {
				t.Fatalf("message has %d lines, want <= %d:\n%s", len(lines), size.height, out)
			}
			for _, line := range lines {
				if got := lipgloss.Width(line); got > size.width {
					t.Fatalf("line width = %d, want <= %d: %q", got, size.width, line)
				}
			}
			want := fmt.Sprintf("Terminal too small. Resize to at least 40x10 (current: %dx%d).", size.width, size.height)
			if got := strings.Join(strings.Fields(out), " "); got != want {
				t.Fatalf("message = %q, want %q", got, want)
			}
		})
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
	m.info.visible = true

	m.handleKey(tea.KeyPressMsg{Text: "j"})
	if m.info.scroll == 0 {
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
		{name: "provider search", set: func(m *Model) { m.searchOverlay.visible = true }},
		{name: "navigation", set: func(m *Model) { m.navBrowser.visible = true }},
		{name: "playlist manager", set: func(m *Model) { m.plManager.visible = true }},
		{name: "queue", set: func(m *Model) { m.queue.visible = true }},
		{name: "info", set: func(m *Model) { m.info.visible = true }},
		{name: "lyrics", set: func(m *Model) { m.lyrics.visible = true }},
		{name: "jump", set: func(m *Model) { m.jump.active = true }},
		{name: "url", set: func(m *Model) { m.urlInput.active = true }},
		{name: "search", set: func(m *Model) { m.search.active = true }},
		{name: "online search", set: func(m *Model) { m.netSearch.active = true }},
	}

	for _, size := range []struct{ width, height int }{{40, 10}, {56, 16}, {80, 24}} {
		for _, overlay := range overlays {
			t.Run(fmt.Sprintf("%s_%dx%d", overlay.name, size.width, size.height), func(t *testing.T) {
				m := newLayoutTestModel(size.width, size.height)
				overlay.set(&m)
				m.recomputeLayout()
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

func TestConfiguredVisualizerRows(t *testing.T) {
	tests := []struct {
		name    string
		width   int
		height  int
		visRows int
		want    int
	}{
		{name: "unset keeps the default", width: 120, height: 50, visRows: 0, want: ui.DefaultVisRows},
		{name: "taller than the default", width: 120, height: 50, visRows: 20, want: 20},
		{name: "shorter than the default", width: 120, height: 50, visRows: 2, want: 2},
		{name: "capped by a short terminal", width: 120, height: 24, visRows: 40, want: 9},
		{name: "compact tier is unaffected", width: 60, height: 16, visRows: 20, want: compactVisRows},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := newLayoutTestModel(tt.width, tt.height)
			m.SetVisRows(tt.visRows)
			if m.vis.Rows != tt.want {
				t.Fatalf("visualizer rows = %d, want %d", m.vis.Rows, tt.want)
			}
			if m.layout.bodyRows < 1 {
				t.Fatalf("body rows = %d, want at least one", m.layout.bodyRows)
			}
			out := m.View().Content
			if got := lipgloss.Height(out); got > tt.height {
				t.Fatalf("view height = %d, want <= %d", got, tt.height)
			}
		})
	}
}

// Simplified mode drops its own layout as soon as the provider or an overlay
// takes focus, and those screens do draw a list. SetExpanded must therefore
// reach heightExpanded there exactly as Ctrl+X does, or --simplified --expanded
// would do nothing on the only screens where it shows.
func TestSetExpandedAppliesToSimplifiedProviderLists(t *testing.T) {
	m := newLayoutTestModel(100, 50)
	m.SetSimplified(true)
	m.focus = focusProvider
	m.recomputeLayout()

	collapsed := m.plVisible
	if collapsed == 0 {
		t.Fatal("provider list rows = 0 in simplified mode, want the content-first list")
	}

	m.SetExpanded(true)
	if !m.heightExpanded {
		t.Fatal("SetExpanded did not set the expanded height in simplified mode")
	}
	if m.plVisible != m.layout.bodyRows {
		t.Fatalf("expanded provider list rows = %d, want available body rows %d", m.plVisible, m.layout.bodyRows)
	}
	if m.plVisible <= collapsed {
		t.Fatalf("expanded provider list rows = %d, want more than collapsed %d", m.plVisible, collapsed)
	}

	// And it matches what the key produces from the same state.
	byKey := newLayoutTestModel(100, 50)
	byKey.SetSimplified(true)
	byKey.focus = focusProvider
	byKey.recomputeLayout()
	byKey.toggleExpandedView()
	if byKey.plVisible != m.plVisible {
		t.Fatalf("Ctrl+X rows = %d, SetExpanded rows = %d, want the same", byKey.plVisible, m.plVisible)
	}
}

// The playback screen has no playlist in simplified mode, so the height is
// carried but ignored rather than blocked.
func TestSetExpandedIsInertOnTheSimplifiedPlaybackScreen(t *testing.T) {
	m := newLayoutTestModel(100, 50)
	m.SetSimplified(true)
	m.SetExpanded(true)
	if m.plVisible != 0 {
		t.Fatalf("simplified playback playlist rows = %d, want 0", m.plVisible)
	}
}

// TestUpdateKeepsLayoutCurrent checks that the layout that View reads is the
// one the state asks for after every message, so that View does not have to
// lay out the frame itself.
func TestUpdateKeepsLayoutCurrent(t *testing.T) {
	m := newLayoutTestModel(80, 24)
	openDevicePicker := func(m *Model) {
		m.devicePicker.visible, m.devicePicker.loading = true, true
		m.recomputeLayout()
	}
	msgs := []struct {
		name   string
		before func(*Model)
		msg    tea.Msg
	}{
		{name: "resize to the compact tier", msg: tea.WindowSizeMsg{Width: 60, Height: 18}},
		{name: "resize to the full tier", msg: tea.WindowSizeMsg{Width: 120, Height: 40}},
		{name: "open the keymap", msg: tea.KeyPressMsg{Code: 'k', Mod: tea.ModCtrl}},
		{name: "close the keymap", msg: tea.KeyPressMsg{Code: tea.KeyEscape}},
		{name: "full-screen visualizer", msg: tea.KeyPressMsg{Code: 'V', Text: "V"}},
		{name: "leave the full-screen visualizer", msg: tea.KeyPressMsg{Code: tea.KeyEscape}},
		{name: "status message", msg: ShowStatusMsg{Text: "Saved"}},
		// A failed device list closes the picker outside the key path.
		{name: "device list failed", before: openDevicePicker, msg: devicesListedMsg{err: errors.New("no devices")}},
		{name: "resize to the minimal tier", msg: tea.WindowSizeMsg{Width: 45, Height: 12}},
	}
	for _, step := range msgs {
		if step.before != nil {
			step.before(&m)
		}
		updated, _ := m.Update(step.msg)
		m = updated.(Model)
		want := m
		want.recomputeLayout()
		if m.layout != want.layout || m.plVisible != want.plVisible {
			t.Fatalf("after %s: layout = %+v, plVisible %d; want %+v, plVisible %d", step.name, m.layout, m.plVisible, want.layout, want.plVisible)
		}
	}
}

// TestViewLeavesLayoutState checks that View reads the layout and the
// visualizer size and never writes them.
func TestViewLeavesLayoutState(t *testing.T) {
	m := newLayoutTestModel(100, 30)
	layout := m.layout
	m.vis.Cols, m.vis.Rows = 7, 3

	m.View()

	if m.vis.Cols != 7 || m.vis.Rows != 3 {
		t.Fatalf("visualizer size after View = %dx%d, want 7x3", m.vis.Cols, m.vis.Rows)
	}
	if m.layout != layout {
		t.Fatalf("layout after View = %+v, want %+v", m.layout, layout)
	}
}
