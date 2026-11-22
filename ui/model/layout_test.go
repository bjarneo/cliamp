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
	m.SetFrameBase(defaultFrameBaseWidth, defaultFrameBaseHeight)
	m.recomputeLayout()
	return m
}

// TestFrameLayoutRows pins the row budget: the frame is defaultFrameBaseHeight
// rows plus vis_rows, and defaultFrameBaseWidth columns, or the whole
// terminal when expanded, the spectrum never takes more rows than the body,
// and the body takes what the other sections leave.
func TestFrameLayoutRows(t *testing.T) {
	type rows struct{ height, title, spectrum, settings, gap, help, body int }
	tests := []struct {
		name          string
		width, height int
		setup         func(*Model)
		want          rows
		twoColumn     bool
	}{
		{name: "stacked settings hide under 10 rows", width: 40, height: 10, want: rows{height: 10, help: 1, body: 5}},
		{name: "compact spectrum under 25 rows", width: 80, height: 24, twoColumn: true, want: rows{height: 24, title: 1, spectrum: 3, help: 1, body: 15}},
		{name: "default frame", width: 100, height: 30, twoColumn: true, want: rows{height: 30, title: 1, spectrum: 7, gap: 1, help: 1, body: 16}},
		{name: "tall terminal keeps the frame", width: 100, height: 60, twoColumn: true, want: rows{height: 31, title: 1, spectrum: 7, gap: 1, help: 1, body: 17}},
		{name: "one stacked row over 90 columns", width: 100, height: 30, setup: func(m *Model) { m.hideSettings = true },
			want: rows{height: 30, title: 1, spectrum: 7, settings: 1, gap: 1, help: 1, body: 15}},
		{name: "one stacked row from 80 columns", width: 80, height: 30, setup: func(m *Model) { m.hideSettings = true },
			want: rows{height: 30, title: 1, spectrum: 7, settings: 1, gap: 1, help: 1, body: 15}},
		{name: "expanded", width: 120, height: 50, twoColumn: true, setup: func(m *Model) { m.heightExpanded = true },
			want: rows{height: 50, title: 1, spectrum: ui.DefaultVisRows, gap: 1, help: 1, body: 36}},
		{name: "tall vis_rows", width: 120, height: 50, twoColumn: true, setup: func(m *Model) { m.visRows = 12 },
			want: rows{height: 36, title: 1, spectrum: 12, gap: 1, help: 1, body: 17}},
		{name: "spectrum never outgrows the body", width: 100, height: 31, twoColumn: true, setup: func(m *Model) { m.visRows = 20 },
			want: rows{height: 31, title: 1, spectrum: 12, gap: 1, help: 1, body: 12}},
		{name: "visualizer off", width: 100, height: 30, twoColumn: true, setup: func(m *Model) { m.vis.Mode = ui.VisNone },
			want: rows{height: 30, title: 1, gap: 1, help: 1, body: 23}},
		{name: "help bar hidden", width: 100, height: 30, twoColumn: true, setup: func(m *Model) { m.hideHelpBar = true },
			want: rows{height: 30, title: 1, spectrum: 7, gap: 1, body: 17}},
		{name: "list view", width: 100, height: 30, setup: func(m *Model) { m.focus = focusProvider; m.provider = &readOnlyGenreProvider{} },
			want: rows{height: 30, title: 1, gap: 1, help: 1, body: 23}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := newLayoutTestModel(tt.width, tt.height)
			if tt.setup != nil {
				tt.setup(&m)
				m.recomputeLayout()
			}
			l := m.layout
			got := rows{l.height, l.titleRows, l.spectrumRows, l.settingsRows, l.gapRows, l.helpRows, l.bodyRows}
			if got != tt.want || l.twoColumn != tt.twoColumn {
				t.Fatalf("rows = %+v, twoColumn = %v; want %+v, %v", got, l.twoColumn, tt.want, tt.twoColumn)
			}
			if m.plVisible != l.listRows() {
				t.Fatalf("playlist rows = %d, want %d", m.plVisible, l.listRows())
			}
			if l.spectrumRows > 0 && m.vis.Rows != l.spectrumRows {
				t.Fatalf("visualizer rows = %d, want %d", m.vis.Rows, l.spectrumRows)
			}
			if m.vis.Cols != l.panelWidth {
				t.Fatalf("visualizer columns = %d, want %d", m.vis.Cols, l.panelWidth)
			}
		})
	}
}

// TestFrameFillsLayoutHeight checks that the sections render exactly the
// layout height, so the transient and the help bar sit at the bottom.
func TestFrameFillsLayoutHeight(t *testing.T) {
	for _, size := range []struct{ width, height int }{{40, 10}, {56, 16}, {80, 24}, {100, 30}, {120, 50}} {
		for _, hide := range []bool{false, true} {
			t.Run(fmt.Sprintf("%dx%d/hidePane=%v", size.width, size.height, hide), func(t *testing.T) {
				m := newColumnTestModel(size.width, size.height)
				m.SetHideSettingsPane(hide)
				content := strings.Join(m.mainSections(m.renderBodyRegion()), "\n")
				if got := lipgloss.Height(content); got != m.layout.height {
					t.Fatalf("frame rows = %d, want %d\n%s", got, m.layout.height, content)
				}
			})
		}
	}
}

func TestFrameLayoutWidthCap(t *testing.T) {
	m := newLayoutTestModel(160, 30)
	if m.layout.frameWidth != defaultFrameBaseWidth {
		t.Fatalf("frame width = %d, want %d", m.layout.frameWidth, defaultFrameBaseWidth)
	}
	wantPanel := defaultFrameBaseWidth
	if m.layout.panelWidth != wantPanel {
		t.Fatalf("panel width = %d, want %d", m.layout.panelWidth, wantPanel)
	}

	m.heightExpanded = true
	m.recomputeLayout()
	if m.layout.frameWidth != 160 {
		t.Fatalf("expanded frame width = %d, want 160", m.layout.frameWidth)
	}
}

func TestTooSmallLayout(t *testing.T) {
	m := newLayoutTestModel(39, 9)
	if !m.layout.tooSmall() || m.effectivePlaylistVisible() != 0 {
		t.Fatalf("tooSmall = %v, playlist rows = %d; want true, 0", m.layout.tooSmall(), m.effectivePlaylistVisible())
	}
}

func TestResponsiveViewsFitTerminal(t *testing.T) {
	// Provider count matters: the source row is only drawn with more than one,
	// so a single-provider model never exercises the tallest chrome. Pane
	// state matters for the same reason on wide frames.
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
	collapsed := m.plVisible

	m.heightExpanded = true
	m.recomputeLayout()
	if m.layout.height != 50 {
		t.Fatalf("expanded frame rows = %d, want the terminal's 50", m.layout.height)
	}
	if m.plVisible != m.layout.listRows() || m.plVisible <= collapsed {
		t.Fatalf("expanded playlist rows = %d, want %d and more than %d", m.plVisible, m.layout.listRows(), collapsed)
	}
}

func TestExpandedPlaylistWithoutVisualizerFillsTerminal(t *testing.T) {
	for _, size := range []struct {
		width, height int
	}{
		{width: 80, height: 50},
		{width: 80, height: 30},
		{width: 56, height: 20},
	} {
		t.Run(fmt.Sprintf("%dx%d", size.width, size.height), func(t *testing.T) {
			mWithVis := newLayoutTestModel(size.width, size.height)
			mWithVis.heightExpanded = true
			mWithVis.recomputeLayout()
			bodyRowsWithVis := mWithVis.layout.bodyRows
			spectrumRows := mWithVis.layout.spectrumRows

			m := newLayoutTestModel(size.width, size.height)
			for i := 16; i < 100; i++ {
				m.playlist.Add(playlist.Track{Path: fmt.Sprintf("/tmp/track-%d.mp3", i), Title: "Track"})
			}
			m.providers = []provider.Entry{{Name: "Local"}, {Name: "Radio"}}
			m.vis.Mode = ui.VisNone
			m.heightExpanded = true
			m.recomputeLayout()

			if m.layout.bodyRows != bodyRowsWithVis+spectrumRows {
				t.Fatalf("body rows = %d, want %d (%d more than with visualizer)", m.layout.bodyRows, bodyRowsWithVis+spectrumRows, spectrumRows)
			}
			if m.plVisible != m.layout.listRows() {
				t.Fatalf("expanded playlist rows = %d, want available list rows %d", m.plVisible, m.layout.listRows())
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
	content := strings.Join(m.mainSections(m.renderBodyRegion()), "\n")
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
	playback := newLayoutTestModel(100, 30)
	browse := newLayoutTestModel(100, 30)
	browse.keymap.visible = true
	browse.keymap.entries = browse.buildKeymapEntries()
	browse.recomputeLayout()

	if !browse.usesContentFirstLayout() {
		t.Fatal("keymap must use the content-first layout")
	}
	if browse.layout.bodyRows <= playback.layout.bodyRows {
		t.Fatalf("content-first body rows = %d, want more than playback rows %d", browse.layout.bodyRows, playback.layout.bodyRows)
	}
	if browse.layout.spectrumRows != 0 || browse.layout.twoColumn || browse.layout.settingsRows != 0 {
		t.Fatalf("content-first layout = %+v, want no spectrum and no settings", browse.layout)
	}
	if browse.layout.height != playback.layout.height {
		t.Fatalf("content-first frame rows = %d, want the playback frame's %d", browse.layout.height, playback.layout.height)
	}
	if browse.vis.Rows < 1 {
		t.Fatalf("content-first visualizer canvas rows = %d, want positive", browse.vis.Rows)
	}

	preview := newLayoutTestModel(100, 30)
	preview.visPicker.visible = true
	preview.visPicker.modes = preview.vis.AllModeNames()
	preview.recomputeLayout()
	if preview.usesContentFirstLayout() {
		t.Fatal("visualizer picker must retain the live preview layout")
	}
	if preview.layout.spectrumRows == 0 {
		t.Fatal("visualizer picker must retain visualizer rows")
	}
}

func TestResizeHidesVisualizerAndRestoresCanvas(t *testing.T) {
	m := newLayoutTestModel(100, 30)
	p := m.player.(*playbackFakeEngine)
	p.playing = true
	m.vis.Mode = ui.VisScope
	t0 := time.Unix(1, 0)
	m.tickVisualizer(t0)
	before := m.vis.Frame()

	updated, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 19})
	m = updated.(Model)
	if m.visualizerVisible() {
		t.Fatal("visualizerVisible() = true under 20 frame rows, want false")
	}
	if m.vis.Rows < 1 {
		t.Fatalf("hidden visualizer canvas rows = %d, want positive", m.vis.Rows)
	}
	m.tickVisualizer(t0.Add(time.Second))
	if got := m.vis.Frame(); got != before {
		t.Fatalf("hidden visualizer frame = %d, want %d", got, before)
	}

	updated, _ = m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	m = updated.(Model)
	if !m.visualizerVisible() {
		t.Fatal("visualizerVisible() = false after resize, want true")
	}
	if m.vis.Rows != ui.DefaultVisRows {
		t.Fatalf("restored visualizer rows = %d, want %d", m.vis.Rows, ui.DefaultVisRows)
	}
	m.tickVisualizer(t0.Add(2 * time.Second))
	if got := m.vis.Frame(); got != before+1 {
		t.Fatalf("restored visualizer frame = %d, want %d without hidden-time catch-up", got, before+1)
	}
}

func TestTooSmallLayoutRejectsHiddenMainFocus(t *testing.T) {
	m := newLayoutTestModel(80, 24)
	m.focus = focusEQ
	m.prevFocus = focusProvPill

	updated, _ := m.Update(tea.WindowSizeMsg{Width: 39, Height: 9})
	m = updated.(Model)
	if m.focus != focusPlaylist {
		t.Fatalf("focus = %v, want playlist on a too-small terminal", m.focus)
	}
	if m.prevFocus != focusPlaylist {
		t.Fatalf("previous focus = %v, want playlist on a too-small terminal", m.prevFocus)
	}
}

func TestStackedEqualizerShowsActiveBand(t *testing.T) {
	m := newLayoutTestModel(56, 16)
	m.focus = focusEQ
	m.eqCursor = 4

	if plain := stripAnsi(m.renderSettingsStacked()); !strings.Contains(plain, "1k") {
		t.Fatalf("stacked settings = %q, want active EQ band", plain)
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
	if got := len(m.mainSections("")); m.layout.bodyRows != 0 || got != 4 {
		t.Fatalf("simplified body rows = %d, sections = %d; want 0 and track, time, seek, transient", m.layout.bodyRows, got)
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

func TestNetSearchUsesContentFirstLayout(t *testing.T) {
	m := newLayoutTestModel(80, 24)
	m.netSearch.active = true
	m.netSearch.screen = netSearchInput
	m.netSearch.request = "ambient"
	m.requests.netSearch = 1
	m.recomputeLayout()
	if !m.usesContentFirstLayout() {
		t.Fatal("search input must use the content-first layout")
	}
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
	if m.layout.bodyRows != inputRows {
		t.Fatalf("result body rows = %d, want same as input rows %d", m.layout.bodyRows, inputRows)
	}
}

// The frame padding belongs to the Model. A Model that nobody configured
// uses the config defaults. A headless Model keeps the padding for the
// width of its visualizer.
func TestSetPadding(t *testing.T) {
	tests := []struct {
		name               string
		setup              func(*Model)
		wantH, wantV, cols int
	}{
		{name: "unset", setup: func(*Model) {}, wantH: 0, wantV: 0, cols: 80},
		{name: "zero", setup: func(m *Model) { m.SetPadding(0, 0) }, wantH: 0, wantV: 0, cols: 80},
		{name: "configured", setup: func(m *Model) { m.SetPadding(5, 2) }, wantH: 5, wantV: 2, cols: 70},
		{name: "headless", setup: func(m *Model) {
			m.width, m.height = 0, 0
			m.SetPadding(5, 2)
			m.SetHeadless(true)
		}, wantH: 5, wantV: 2, cols: 70},
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
		{200, 50, []string{"init myvis 7x150", "render myvis 7x150"}},
		{100, 30, []string{"init myvis 7x100", "render myvis 7x100"}},
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
	m := newLayoutTestModel(40, 11)
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
		name     string
		width    int
		height   int
		visRows  int
		expanded bool
		want     int
	}{
		{name: "the default", width: 120, height: 50, visRows: 0, want: ui.DefaultVisRows},
		{name: "expanded keeps the default", width: 120, height: 50, visRows: 0, expanded: true, want: ui.DefaultVisRows},
		{name: "taller than the body", width: 120, height: 50, visRows: 20, want: 18},
		{name: "a frame of 34 rows", width: 120, height: 50, visRows: 10, want: 10},
		{name: "a small vis_rows", width: 120, height: 50, visRows: 2, want: 2},
		{name: "a short terminal", width: 120, height: 24, visRows: 40, want: compactSpectrumRows},
		{name: "a terminal under 46 rows", width: 120, height: 30, visRows: 20, want: 11},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := newLayoutTestModel(tt.width, tt.height)
			m.heightExpanded = tt.expanded
			m.SetVisRows(tt.visRows)
			if m.layout.spectrumRows != tt.want {
				t.Fatalf("spectrum rows = %d, want %d", m.layout.spectrumRows, tt.want)
			}
			if m.layout.bodyRows < 1 {
				t.Fatalf("body rows = %d, want at least one", m.layout.bodyRows)
			}
			assertViewFits(t, m.View().Content, tt.width, tt.height)
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
	if m.plVisible != m.layout.listRows() {
		t.Fatalf("expanded provider list rows = %d, want available list rows %d", m.plVisible, m.layout.listRows())
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

func TestToggleExpandedPersists(t *testing.T) {
	saver := &recordingSaver{}
	m := newLayoutTestModel(100, 50)
	m.configSaver = saver

	m.toggleExpandedView()
	if got := saver.saved["expanded"]; got != "true" {
		t.Fatalf("saved expanded = %q, want %q", got, "true")
	}

	m.toggleExpandedView()
	if got := saver.saved["expanded"]; got != "false" {
		t.Fatalf("saved expanded = %q, want %q", got, "false")
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
		{name: "resize to a small terminal", msg: tea.WindowSizeMsg{Width: 60, Height: 18}},
		{name: "resize to a large terminal", msg: tea.WindowSizeMsg{Width: 120, Height: 40}},
		{name: "open the keymap", msg: tea.KeyPressMsg{Code: 'k', Mod: tea.ModCtrl}},
		{name: "close the keymap", msg: tea.KeyPressMsg{Code: tea.KeyEscape}},
		{name: "full-screen visualizer", msg: tea.KeyPressMsg{Code: 'V', Text: "V"}},
		{name: "leave the full-screen visualizer", msg: tea.KeyPressMsg{Code: tea.KeyEscape}},
		{name: "status message", msg: ShowStatusMsg{Text: "Saved"}},
		// A failed device list closes the picker outside the key path.
		{name: "device list failed", before: openDevicePicker, msg: devicesListedMsg{err: errors.New("no devices")}},
		{name: "resize to a tiny terminal", msg: tea.WindowSizeMsg{Width: 45, Height: 12}},
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
