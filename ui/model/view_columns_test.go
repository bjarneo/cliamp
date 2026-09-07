package model

import (
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/bjarneo/cliamp/ui"
)

// newColumnTestModel builds a full-tier model with several providers so the
// settings pane renders every row it can.
func newColumnTestModel(width, height int) Model {
	m := newLayoutTestModel(width, height)
	m.providers = []ProviderEntry{{Name: "Local"}, {Name: "Navidrome"}, {Name: "Radio"}}
	m.eqPresetIdx = 1
	m.applyEQPreset()
	m.recomputeLayout()
	return m
}

// TestTwoColumnLayoutTiers checks that the split engages only where there is
// room for it and the playback chrome it replaces is actually drawn.
func TestTwoColumnLayoutTiers(t *testing.T) {
	tests := []struct {
		name   string
		width  int
		height int
		setup  func(*Model)
		want   bool
	}{
		{name: "full", width: 80, height: 24, want: true},
		{name: "wide", width: 160, height: 48, want: true},
		{name: "compact", width: 56, height: 16, want: false},
		{name: "minimal", width: 40, height: 10, want: false},
		{name: "too small", width: 20, height: 5, want: false},
		{name: "simplified", width: 80, height: 24, want: false, setup: func(m *Model) { m.simplified = true }},
		{name: "overlay", width: 80, height: 24, want: false, setup: func(m *Model) { m.queue.visible = true }},
		{name: "full screen visualizer", width: 80, height: 24, want: false, setup: func(m *Model) { m.fullVis = true }},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := newColumnTestModel(tt.width, tt.height)
			if tt.setup != nil {
				tt.setup(&m)
				m.recomputeLayout()
			}
			if got := m.layout.twoColumn; got != tt.want {
				t.Fatalf("twoColumn = %v, want %v", got, tt.want)
			}
			if !tt.want {
				return
			}
			if sum := m.layout.playlistWidth + columnGutterWidth + m.layout.settingsWidth; sum != m.layout.panelWidth {
				t.Fatalf("column widths sum to %d, want panel width %d", sum, m.layout.panelWidth)
			}
			if m.layout.playlistWidth < playlistMinWidth {
				t.Fatalf("playlist column = %d, want at least %d", m.layout.playlistWidth, playlistMinWidth)
			}
		})
	}
}

// cells returns a rendered row as display cells, so a wide character in a
// track title occupies the two columns it actually paints.
func cells(row string) []rune {
	var out []rune
	for _, r := range ansi.Strip(row) {
		out = append(out, r)
		for range lipgloss.Width(string(r)) - 1 {
			out = append(out, ' ')
		}
	}
	return out
}

// TestTwoColumnGutterStaysBlank checks the invariant that replaces a drawn
// rule: the gutter columns are empty on the header and on every body row, so
// the blank channel between the columns runs unbroken down the body.
func TestTwoColumnGutterStaysBlank(t *testing.T) {
	for _, size := range []struct{ width, height int }{{80, 24}, {100, 30}, {160, 48}} {
		t.Run(fmt.Sprintf("%dx%d", size.width, size.height), func(t *testing.T) {
			m := newColumnTestModel(size.width, size.height)
			rows := append([]string{m.renderColumnHeaders()}, strings.Split(m.renderBodyRegion(), "\n")...)

			for i, row := range rows {
				got := cells(row)
				if len(got) < m.layout.playlistWidth+columnGutterWidth {
					t.Fatalf("row %d is %d cells wide, too narrow to hold the gutter: %q", i, len(got), ansi.Strip(row))
				}
				gutter := got[m.layout.playlistWidth : m.layout.playlistWidth+columnGutterWidth]
				if strings.TrimSpace(string(gutter)) != "" {
					t.Fatalf("row %d gutter = %q, want blank in %q", i, string(gutter), ansi.Strip(row))
				}
			}
		})
	}
}

// TestTwoColumnBodyRowsMatch checks that both columns render the same number of
// rows, which is what keeps the rule unbroken as the playlist grows.
func TestTwoColumnBodyRowsMatch(t *testing.T) {
	m := newColumnTestModel(100, 30)
	rows := m.effectivePlaylistVisible()
	if got := len(strings.Split(m.renderBodyRegion(), "\n")); got != rows {
		t.Fatalf("body rows = %d, want %d", got, rows)
	}
	if got := len(strings.Split(m.renderSettingsPane(rows), "\n")); got != rows {
		t.Fatalf("settings pane rows = %d, want %d", got, rows)
	}
}

// TestSettingsPaneRows checks that every setting Tab cycles through reaches the
// pane, and that the source row is dropped when there is nothing to switch to.
func TestSettingsPaneRows(t *testing.T) {
	tests := []struct {
		name      string
		providers []ProviderEntry
		want      []string
		absent    []string
	}{
		{
			name:      "multiple providers",
			providers: []ProviderEntry{{Name: "Local"}, {Name: "Navidrome"}},
			want:      []string{"EQ", "[Rock]", "VOL", "+0dB", "SRC", "[Local] 1/2", "SPD", "[1x]", "SHF", "RPT"},
		},
		{
			name:      "single provider",
			providers: []ProviderEntry{{Name: "Local"}},
			want:      []string{"EQ", "VOL", "SPD", "SHF", "RPT"},
			absent:    []string{"SRC"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := newColumnTestModel(100, 30)
			m.providers = tt.providers
			m.recomputeLayout()

			pane := ansi.Strip(m.renderSettingsPane(m.effectivePlaylistVisible()))
			for _, want := range tt.want {
				if !strings.Contains(pane, want) {
					t.Fatalf("settings pane missing %q:\n%s", want, pane)
				}
			}
			for _, absent := range tt.absent {
				if strings.Contains(pane, absent) {
					t.Fatalf("settings pane unexpectedly contains %q:\n%s", absent, pane)
				}
			}
		})
	}
}

// TestSettingsPaneShowsAllEQBands checks that splitting the bands across two
// rows still shows all ten, since the column cannot fit them on one row.
func TestSettingsPaneShowsAllEQBands(t *testing.T) {
	// A band left at 0 dB shows its frequency, so the flat model is the one
	// that renders every label.
	m := newLayoutTestModel(100, 30)

	rows := m.settingsEQBands()
	if len(rows) != 2 {
		t.Fatalf("band rows = %d, want 2", len(rows))
	}
	joined := ansi.Strip(strings.Join(rows, " "))
	for _, label := range eqBandLabels {
		if !strings.Contains(joined, label) {
			t.Fatalf("band %q missing from %q", label, joined)
		}
	}
	for _, row := range rows {
		if got, want := lipgloss.Width(ansi.Strip(row)), len(eqBandIndent)+5*eqBandCellWidth+4; got != want {
			t.Fatalf("band row width = %d, want %d: %q", got, want, row)
		}
	}
}

// TestTwoColumnDropsStackedChrome checks that the rows the settings pane took
// over are not also drawn above and below the playlist.
func TestTwoColumnDropsStackedChrome(t *testing.T) {
	m := newColumnTestModel(100, 30)
	if !m.layout.twoColumn {
		t.Fatal("expected the two-column layout at 100x30")
	}

	sections := m.mainSections(m.renderBodyRegion(), false, false)
	for _, unwanted := range []string{m.renderControls(), m.renderProviderPill(), m.renderBottomStatus()} {
		if unwanted == "" {
			continue
		}
		for _, section := range sections {
			if section == unwanted {
				t.Fatalf("stacked chrome row still rendered: %q", ansi.Strip(section))
			}
		}
	}
}

// TestTwoColumnPlaylistUsesReclaimedRows checks that the rows freed by moving
// the controls into the pane go to the playlist instead of staying blank.
func TestTwoColumnPlaylistUsesReclaimedRows(t *testing.T) {
	m := newColumnTestModel(100, 60)
	if !m.layout.twoColumn {
		t.Fatal("expected the two-column layout at 100x60")
	}
	if got, want := m.plVisible, maxPlVisible+twoColumnChromeRows; got != want {
		t.Fatalf("playlist rows = %d, want %d", got, want)
	}
}

// TestTwoColumnPlaylistRendersAtColumnWidth checks that the playlist lays out
// inside its column rather than at the full panel width, and that the global
// panel width is restored afterwards for the full-width chrome.
func TestTwoColumnPlaylistRendersAtColumnWidth(t *testing.T) {
	m := newColumnTestModel(100, 30)
	before := ui.PanelWidth

	for _, line := range strings.Split(m.renderBodyRegion(), "\n") {
		if got, want := lipgloss.Width(line), m.layout.panelWidth; got != want {
			t.Fatalf("body row width = %d, want %d: %q", got, want, ansi.Strip(line))
		}
	}
	if ui.PanelWidth != before {
		t.Fatalf("panel width left at %d, want %d restored", ui.PanelWidth, before)
	}
}

// TestMarkerColumnsReserveOnlyWhatIsUsed checks that the playlist reserves a
// state column only once something can appear in it, which is what lets the
// titles start further left on a plain playlist.
func TestMarkerColumnsReserveOnlyWhatIsUsed(t *testing.T) {
	tests := []struct {
		name  string
		setup func(*Model)
		want  markerColumns
	}{
		{name: "plain playlist", setup: func(*Model) {}},
		{
			name:  "queued track",
			setup: func(m *Model) { m.playlist.Queue(1) },
			want:  markerColumns{queue: true},
		},
		{
			name:  "bookmarked track",
			setup: func(m *Model) { m.playlist.ToggleBookmark(1) },
			want:  markerColumns{bookmark: true},
		},
		{
			name:  "favorite track",
			setup: func(m *Model) { m.favSet = map[string]struct{}{"/tmp/track-1.mp3": {}} },
			want:  markerColumns{favorite: true},
		},
		{
			name: "all three",
			setup: func(m *Model) {
				m.playlist.Queue(1)
				m.playlist.ToggleBookmark(2)
				m.favSet = map[string]struct{}{"/tmp/track-3.mp3": {}}
			},
			want: markerColumns{queue: true, bookmark: true, favorite: true},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := newLayoutTestModel(100, 30)
			tt.setup(&m)
			if got := m.markerColumns(); got != tt.want {
				t.Fatalf("markerColumns() = %+v, want %+v", got, tt.want)
			}
		})
	}
}

// TestPlaylistTitleColumnTightensWhenUnused checks the payoff: an untouched
// playlist starts its track numbers further left than one using every state
// column, and rows stay aligned with each other either way.
func TestPlaylistTitleColumnTightensWhenUnused(t *testing.T) {
	// The fixture titles are the only run of letters in a row, so their start
	// column is the title column. Every row must share it.
	const fixtureTitle = "A very long"
	titleColumn := func(m Model) int {
		rows := strings.Split(ansi.Strip(m.renderPlaylist()), "\n")
		col := -1
		for i, row := range rows {
			byteAt := strings.Index(row, fixtureTitle)
			if byteAt < 0 {
				continue
			}
			// Marker glyphs are multi-byte, so measure the prefix in cells.
			at := lipgloss.Width(row[:byteAt])
			if col < 0 {
				col = at
				continue
			}
			if at != col {
				t.Fatalf("row %d title at column %d, want %d: %q", i+1, at, col, row)
			}
		}
		if col < 0 {
			t.Fatalf("no titled row rendered:\n%s", strings.Join(rows, "\n"))
		}
		return col
	}

	plain := titleColumn(newLayoutTestModel(100, 30))

	full := newLayoutTestModel(100, 30)
	full.playlist.Queue(1)
	full.playlist.ToggleBookmark(2)
	full.favSet = map[string]struct{}{"/tmp/track-3.mp3": {}}
	used := titleColumn(full)

	if plain >= used {
		t.Fatalf("plain playlist starts at column %d, want left of the used one at %d", plain, used)
	}
	if want := used - 3; plain != want {
		t.Fatalf("plain playlist starts at column %d, want %d (three columns reclaimed)", plain, want)
	}
}

// TestPlaylistHeaderDropsBadgesThatDoNotFit checks that a narrow pane sheds
// whole badges off the tail instead of letting one be sliced mid-token.
func TestPlaylistHeaderDropsBadgesThatDoNotFit(t *testing.T) {
	// Enough badges that they cannot all fit a 45-column playlist pane.
	withBadges := func(width, height int) Model {
		m := newColumnTestModel(width, height)
		m.playlist.Queue(1)
		m.playlist.ToggleBookmark(2)
		m.favSet = map[string]struct{}{"/tmp/track-3.mp3": {}}
		return m
	}
	headerAt := func(m Model, width int) string {
		defer ui.WithPanelWidth(width)()
		return ansi.Strip(m.renderPlaylistHeader())
	}

	m := withBadges(80, 24)
	header := headerAt(m, m.layout.playlistWidth)

	if got := lipgloss.Width(header); got > m.layout.playlistWidth {
		t.Fatalf("header width = %d, want at most %d: %q", got, m.layout.playlistWidth, header)
	}
	if strings.Count(header, "[") != strings.Count(header, "]") {
		t.Fatalf("header has an unclosed badge: %q", header)
	}
	if !strings.HasSuffix(header, "──") {
		t.Fatalf("header should end in separator fill, not a clipped badge: %q", header)
	}

	// Same state in a pane with room keeps more badges, so the narrow header
	// really is dropping them rather than never having built them.
	wide := withBadges(200, 40)
	if narrow, all := strings.Count(header, "["), strings.Count(headerAt(wide, wide.layout.playlistWidth), "["); narrow >= all {
		t.Fatalf("narrow header kept %d badges, wide kept %d", narrow, all)
	}
}

// TestSettingsPaneOrder pins the signal-chain reading order: where the audio
// comes from, how loud and how shaped it is, how the list plays, and last what
// the stream is doing.
func TestSettingsPaneOrder(t *testing.T) {
	m := newColumnTestModel(100, 30)
	pane := ansi.Strip(m.renderSettingsPane(settingsPaneMaxRows))

	at := -1
	for _, label := range []string{"SRC", "VOL", "EQ", "SHF", "RPT", "SPD"} {
		got := strings.Index(pane, label)
		if got < 0 {
			t.Fatalf("pane is missing %q:\n%s", label, pane)
		}
		if got < at {
			t.Fatalf("%q appears before the row above it:\n%s", label, pane)
		}
		at = got
	}
}

// TestSettingsPaneShedsByRankNotPosition checks that a body squeezed by a tall
// visualizer gives up the read-only counters and the EQ band detail before any
// setting the listener can change, whatever order those rows are drawn in.
func TestSettingsPaneShedsByRankNotPosition(t *testing.T) {
	tests := []struct {
		rows     int
		wantKept []string
		wantGone []string
	}{
		{rows: settingsPaneMaxRows, wantKept: []string{"SRC", "VOL", "EQ", "SHF", "RPT", "SPD"}},
		{rows: 8, wantKept: []string{"SRC", "VOL", "EQ", "SHF", "RPT", "SPD"}},
		// Too short for the band rows: they go, the controls and modes stay.
		{rows: 6, wantKept: []string{"SRC", "VOL", "EQ", "SHF", "RPT", "SPD"}, wantGone: []string{"+5"}},
		// Shorter still: the modes go before anything focusable.
		{rows: 4, wantKept: []string{"SRC", "VOL", "EQ", "SPD"}, wantGone: []string{"+5", "SHF", "RPT"}},
	}

	for _, tt := range tests {
		t.Run(fmt.Sprintf("%drows", tt.rows), func(t *testing.T) {
			m := newColumnTestModel(100, 30)
			pane := ansi.Strip(m.renderSettingsPane(tt.rows))

			if got := len(strings.Split(pane, "\n")); got != tt.rows {
				t.Fatalf("pane rows = %d, want %d", got, tt.rows)
			}
			for _, want := range tt.wantKept {
				if !strings.Contains(pane, want) {
					t.Fatalf("pane at %d rows dropped %q:\n%s", tt.rows, want, pane)
				}
			}
			for _, gone := range tt.wantGone {
				if strings.Contains(pane, gone) {
					t.Fatalf("pane at %d rows should have shed %q first:\n%s", tt.rows, gone, pane)
				}
			}
		})
	}
}

// TestSettingsPaneShedsRankGroupsWhole checks that a rank is dropped entire: a
// lone EQ band row, or shuffle without repeat, would read as a broken render.
func TestSettingsPaneShedsRankGroupsWhole(t *testing.T) {
	m := newColumnTestModel(100, 30)
	// Local playback draws no NET row, so the pane builds eight rows. Giving
	// it seven means it is one over: the band pair goes together rather than
	// leaving one behind, so the pane settles at six and pads the spare row.
	pane := ansi.Strip(m.renderSettingsPane(7))
	if got := len(strings.Split(pane, "\n")); got != 7 {
		t.Fatalf("pane rows = %d, want 7", got)
	}
	if drawn := len(strings.Split(strings.TrimRight(pane, "\n "), "\n")); drawn != 6 {
		t.Fatalf("drawn rows = %d, want 6 after the band pair went together:\n%s", drawn, pane)
	}
	if strings.Contains(pane, "+5") || strings.Contains(pane, "+4") {
		t.Fatalf("expected both band rows gone, found band content:\n%s", pane)
	}
	if strings.Contains(pane, "SHF") != strings.Contains(pane, "RPT") {
		t.Fatalf("shuffle and repeat must appear or vanish together:\n%s", pane)
	}
}

// TestSettingsPaneToggleClosesAndReopens checks that Ctrl+B reaches the toggle
// through the main key path and that it round-trips the layout.
func TestSettingsPaneToggleClosesAndReopens(t *testing.T) {
	m := newColumnTestModel(80, 24)
	m.configSaver = &recordingConfigSaver{}
	if !m.layout.twoColumn {
		t.Fatal("expected the pane open at 80x24")
	}

	m.handleKey(tea.KeyPressMsg{Code: 'b', Mod: tea.ModCtrl})
	if m.layout.twoColumn {
		t.Fatal("ctrl+b did not close the settings pane")
	}
	m.handleKey(tea.KeyPressMsg{Code: 'b', Mod: tea.ModCtrl})
	if !m.layout.twoColumn {
		t.Fatal("ctrl+b did not reopen the settings pane")
	}
}

// TestSettingsPaneTogglePersists checks that the choice is written to config so
// the pane comes back the way it was left.
func TestSettingsPaneTogglePersists(t *testing.T) {
	saver := &recordingConfigSaver{}
	m := newColumnTestModel(80, 24)
	m.configSaver = saver

	m.toggleSettingsPane()
	if got := saver.values["hide_settings_pane"]; got != "true" {
		t.Fatalf("saved hide_settings_pane = %q, want %q", got, "true")
	}
	m.toggleSettingsPane()
	if got := saver.values["hide_settings_pane"]; got != "false" {
		t.Fatalf("saved hide_settings_pane = %q, want %q", got, "false")
	}
}

// TestSettingsPaneOpensByDefault checks the startup state: with no
// hide_settings_pane in the config, the pane is drawn.
func TestSettingsPaneOpensByDefault(t *testing.T) {
	m := newColumnTestModel(80, 24)
	if m.hideSettings {
		t.Fatal("hideSettings defaults to true, want the pane open")
	}
	if !m.layout.twoColumn {
		t.Fatal("want the pane drawn at 80x24 by default")
	}
}

// TestClosedSettingsLayoutKeepsSourceAndVolume pins what the closed pane
// draws: the source and volume readouts share one row, and the EQ, speed, and
// download readouts are gone. Shuffle and repeat return to the playlist header
// because there is no pane holding them.
func TestClosedSettingsLayoutKeepsSourceAndVolume(t *testing.T) {
	m := newColumnTestModel(80, 24)
	m.SetHideSettingsPane(true)
	if m.layout.twoColumn || !m.layout.closedSettings {
		t.Fatalf("want the closed-pane layout, got twoColumn=%v closed=%v", m.layout.twoColumn, m.layout.closedSettings)
	}

	frame := ansi.Strip(m.View().Content)
	for _, want := range []string{"SRC", "VOL", "Shuffle", "Repeat"} {
		if !strings.Contains(frame, want) {
			t.Fatalf("closed layout lost %q:\n%s", want, frame)
		}
	}
	for _, gone := range []string{"EQ", "SPD", "↓"} {
		if strings.Contains(frame, gone) {
			t.Fatalf("closed layout should not draw %q:\n%s", gone, frame)
		}
	}

	// Source and volume share a row rather than taking one each.
	var rows int
	for _, line := range strings.Split(frame, "\n") {
		if strings.Contains(line, "SRC") || strings.Contains(line, "VOL") {
			rows++
		}
	}
	if rows != 1 {
		t.Fatalf("source and volume span %d rows, want 1:\n%s", rows, frame)
	}
}

// TestClosedSettingsLayoutDropsHiddenFocusStops checks that Tab cannot land on
// the EQ or speed while the closed layout draws no readout for them.
func TestClosedSettingsLayoutDropsHiddenFocusStops(t *testing.T) {
	m := newColumnTestModel(80, 24)
	m.SetHideSettingsPane(true)

	for _, gone := range []focusArea{focusEQ, focusSpeed} {
		if m.mainFocusAllowed(gone) {
			t.Fatalf("%v is a Tab stop with no readout drawn", gone.label())
		}
	}
	if !m.mainFocusAllowed(focusProvPill) {
		t.Fatal("the source row is drawn, so it should stay a Tab stop")
	}

	// Focus restored from the open layout is cleared rather than left stranded.
	m.focus = focusEQ
	m.normalizeMainFocus()
	if m.focus != focusPlaylist {
		t.Fatalf("focus = %v, want it reset to the playlist", m.focus.label())
	}
}

// TestClosedSettingsLayoutHandsRowsToPlaylist checks that the rows the dropped
// chrome frees actually show tracks.
func TestClosedSettingsLayoutHandsRowsToPlaylist(t *testing.T) {
	stacked := newColumnTestModel(80, 24)
	stacked.simplified = false
	stacked.recomputeLayout()
	openRows := stacked.layout.bodyRows

	closed := newColumnTestModel(80, 24)
	closed.SetHideSettingsPane(true)

	if closed.layout.chromeRowsFreed() != closedSettingsChromeRows {
		t.Fatalf("freed rows = %d, want %d", closed.layout.chromeRowsFreed(), closedSettingsChromeRows)
	}
	// The pane frees more rows than the closed layout, so the open pane still
	// shows the taller playlist; the closed one must beat the plain stacked
	// layout it replaces.
	if closed.layout.bodyRows >= openRows {
		t.Fatalf("closed body rows = %d, want fewer than the pane's %d", closed.layout.bodyRows, openRows)
	}
	plain := closed
	plain.layout.closedSettings = false
	if closed.layout.bodyRows <= plain.layout.bodyRows-closedSettingsChromeRows {
		t.Fatalf("closed layout did not reclaim its %d rows", closedSettingsChromeRows)
	}
}

// TestSettingsPaneToggleIgnoredBelowFullTier checks that the binding is not
// advertised where there is no pane to open.
func TestSettingsPaneToggleIgnoredBelowFullTier(t *testing.T) {
	for _, size := range []struct{ width, height int }{{56, 16}, {40, 10}} {
		t.Run(fmt.Sprintf("%dx%d", size.width, size.height), func(t *testing.T) {
			m := newColumnTestModel(size.width, size.height)
			if strings.Contains(ansi.Strip(m.commandHelp(commandModeMain)), "Ctrl+B") {
				t.Fatal("settings-pane binding advertised below the full tier")
			}
		})
	}
}

// TestFillSeparator covers the helper the column and closed-pane headers rely
// on: extend to the width, clip past it, and style the fill so it continues
// the rule it is added to rather than reverting to the terminal foreground.
func TestFillSeparator(t *testing.T) {
	tests := []struct {
		name  string
		line  string
		width int
		want  string
	}{
		{name: "pads to width", line: "ab", width: 5, want: "ab───"},
		{name: "exact width is untouched", line: "abcde", width: 5, want: "abcde"},
		{name: "clips past width", line: "abcdefg", width: 5, want: "abcde"},
		{name: "zero width is empty", line: "abc", width: 0, want: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ansi.Strip(fillSeparator(tt.line, tt.width)); got != tt.want {
				t.Fatalf("fillSeparator(%q, %d) = %q, want %q", tt.line, tt.width, got, tt.want)
			}
		})
	}

	t.Run("fill carries the dim style", func(t *testing.T) {
		got := fillSeparator("ab", 6)
		if want := "ab" + dimStyle.Render("────"); got != want {
			t.Fatalf("fill = %q, want the dim-styled run %q", got, want)
		}
	})
}

// TestClosedSettingsHeaderSpansFullWidth checks that with no pane to stop at,
// the playlist header rule runs the whole frame width instead of ending a few
// cells after the badges.
func TestClosedSettingsHeaderSpansFullWidth(t *testing.T) {
	for _, size := range []struct{ width, height int }{{80, 24}, {120, 40}} {
		t.Run(fmt.Sprintf("%dx%d", size.width, size.height), func(t *testing.T) {
			m := newColumnTestModel(size.width, size.height)
			m.SetHideSettingsPane(true)

			var header string
			for _, line := range strings.Split(ansi.Strip(m.View().Content), "\n") {
				if strings.Contains(line, "Playlist ──") {
					// The frame pads both sides; the header is what is inside.
					header = strings.Trim(line, " ")
					break
				}
			}
			if header == "" {
				t.Fatal("no playlist header rendered")
			}
			if got := lipgloss.Width(header); got != m.layout.panelWidth {
				t.Fatalf("header width = %d, want the full panel width %d: %q", got, m.layout.panelWidth, header)
			}
			if !strings.HasSuffix(header, "─") {
				t.Fatalf("header should end in rule fill: %q", header)
			}
		})
	}
}
