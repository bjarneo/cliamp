package model

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/bjarneo/cliamp/provider"
)

// newColumnTestModel builds a model with several providers so the settings
// render every row they can.
func newColumnTestModel(width, height int) Model {
	m := newLayoutTestModel(width, height)
	m.providers = []provider.Entry{{Name: "Local"}, {Name: "Navidrome"}, {Name: "Radio"}}
	m.eqPresetIdx = 1
	m.applyEQPreset()
	m.recomputeLayout()
	return m
}

// TestTwoColumnLayoutSizes checks that the split engages only from an 80-column
// frame, and only on the playback view.
func TestTwoColumnLayoutSizes(t *testing.T) {
	tests := []struct {
		name   string
		width  int
		height int
		setup  func(*Model)
		want   bool
	}{
		{name: "80-column frame", width: 86, height: 24, want: true},
		{name: "wide", width: 160, height: 48, want: true},
		{name: "79-column frame", width: 79, height: 24, want: false},
		{name: "small", width: 40, height: 10, want: false},
		{name: "too small", width: 20, height: 5, want: false},
		{name: "pane closed", width: 160, height: 48, want: false, setup: func(m *Model) { m.hideSettings = true }},
		{name: "simplified", width: 86, height: 24, want: false, setup: func(m *Model) { m.simplified = true }},
		{name: "overlay", width: 86, height: 24, want: false, setup: func(m *Model) { m.fileBrowser.visible = true }},
		// The queue is a view of the playlist, so it keeps the split and the
		// settings pane rather than taking the frame.
		{name: "queue", width: 86, height: 24, want: true, setup: func(m *Model) { m.queue.visible = true }},
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
			if m.layout.settingsWidth != settingsColumnWidth || m.layout.settingsRows != 0 {
				t.Fatalf("settings column = %d, stacked rows = %d; want %d, 0", m.layout.settingsWidth, m.layout.settingsRows, settingsColumnWidth)
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
	for _, size := range []struct{ width, height int }{{86, 24}, {100, 30}, {160, 48}} {
		t.Run(fmt.Sprintf("%dx%d", size.width, size.height), func(t *testing.T) {
			m := newColumnTestModel(size.width, size.height)
			rows := strings.Split(m.renderBodyRegion(), "\n")

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
	rows := m.layout.listRows()
	if got := len(strings.Split(m.renderBodyRegion(), "\n")); got != m.layout.bodyRows {
		t.Fatalf("body rows = %d, want %d", got, m.layout.bodyRows)
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
		providers []provider.Entry
		want      []string
		absent    []string
	}{
		{
			name:      "multiple providers",
			providers: []provider.Entry{{Name: "Local"}, {Name: "Navidrome"}},
			want:      []string{"EQ", "[Rock]", "VOL", "+0dB", "SRC", "[Local] 1/2", "SPD", "[1x]", "SHF", "RPT"},
		},
		{
			name:      "single provider",
			providers: []provider.Entry{{Name: "Local"}},
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

	body := m.renderBodyRegion()
	for _, section := range m.mainSections(body) {
		if section != body && strings.Contains(ansi.Strip(section), "VOL") {
			t.Fatalf("stacked settings still rendered: %q", ansi.Strip(section))
		}
	}
}

// TestTwoColumnPlaylistRendersAtColumnWidth checks that the playlist lays out
// inside its column rather than at the full panel width, and that the panel
// width stays whole for the full-width chrome.
func TestTwoColumnPlaylistRendersAtColumnWidth(t *testing.T) {
	m := newColumnTestModel(100, 30)
	before := m.layout.panelWidth

	for _, line := range strings.Split(m.renderBodyRegion(), "\n") {
		if got, want := lipgloss.Width(line), m.layout.panelWidth; got != want {
			t.Fatalf("body row width = %d, want %d: %q", got, want, ansi.Strip(line))
		}
	}
	if m.layout.panelWidth != before {
		t.Fatalf("panel width left at %d, want %d", m.layout.panelWidth, before)
	}
}

// TestMarkerColumnsReserveOnlyWhatIsUsed checks that the playlist reserves a
// state column only once something can appear in it, which is what lets the
// titles start further left on a plain playlist. The favorite column is the
// exception: it stays pinned so toggling the first/last favorite never shifts
// the titles.
func TestMarkerColumnsReserveOnlyWhatIsUsed(t *testing.T) {
	tests := []struct {
		name  string
		setup func(*Model)
		want  markerColumns
	}{
		{name: "plain playlist", setup: func(*Model) {}, want: markerColumns{favorite: true}},
		{
			name:  "queued track",
			setup: func(m *Model) { m.playlist.Queue(1) },
			want:  markerColumns{queue: true, favorite: true},
		},
		{
			name: "legacy bookmark reserves nothing",
			setup: func(m *Model) {
				track, _ := m.playlist.Track(1)
				track.Bookmark = true
				m.playlist.SetTrack(1, track)
			},
			want: markerColumns{favorite: true},
		},
		{
			name:  "favorite track",
			setup: func(m *Model) { m.favSet = map[string]struct{}{"/tmp/track-1.mp3": {}} },
			want:  markerColumns{favorite: true},
		},
		{
			name: "queue and favorite",
			setup: func(m *Model) {
				m.playlist.Queue(1)
				m.favSet = map[string]struct{}{"/tmp/track-3.mp3": {}}
			},
			want: markerColumns{queue: true, favorite: true},
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
// playlist starts its track numbers further left than one using every dynamic
// state column, and rows stay aligned with each other either way. The favorite
// column stays pinned, so it is excluded from the reclaimed width.
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
	full.favSet = map[string]struct{}{"/tmp/track-3.mp3": {}}
	used := titleColumn(full)

	if plain >= used {
		t.Fatalf("plain playlist starts at column %d, want left of the used one at %d", plain, used)
	}
	if want := used - 1; plain != want {
		t.Fatalf("plain playlist starts at column %d, want %d (one column reclaimed)", plain, want)
	}
}

// TestFavoriteToggleKeepsTitleColumnStable checks the layout-shift fix: adding
// or removing the only favorite must not move the title column, since the
// favorite cell is always reserved.
func TestFavoriteToggleKeepsTitleColumnStable(t *testing.T) {
	const fixtureTitle = "A very long"
	titleColumn := func(m Model) int {
		rows := strings.Split(ansi.Strip(m.renderPlaylist()), "\n")
		for _, row := range rows {
			if byteAt := strings.Index(row, fixtureTitle); byteAt >= 0 {
				return lipgloss.Width(row[:byteAt])
			}
		}
		t.Fatalf("no titled row rendered:\n%s", strings.Join(rows, "\n"))
		return -1
	}

	plain := newLayoutTestModel(100, 30)
	plainCol := titleColumn(plain)

	faved := newLayoutTestModel(100, 30)
	faved.favSet = map[string]struct{}{"/tmp/track-1.mp3": {}}
	if got := titleColumn(faved); got != plainCol {
		t.Fatalf("favorited title at column %d, want stable column %d", got, plainCol)
	}
}

// TestPlaylistHeaderDropsBadgesThatDoNotFit checks that a narrow pane sheds
// whole badges off the tail instead of letting one be sliced mid-token.
func TestPlaylistHeaderDropsBadgesThatDoNotFit(t *testing.T) {
	// A long play-next count makes the Next badge too wide for a narrow pane.
	withBadges := func(width, height int) Model {
		m := newColumnTestModel(width, height)
		for range 999999 {
			m.playlist.Queue(0)
		}
		return m
	}
	headerAt := func(m Model, width int) string {
		m.layout.panelWidth = width
		return ansi.Strip(m.renderPlaylistHeader())
	}

	const narrowWidth = 32
	m := withBadges(86, 24)
	header := headerAt(m, narrowWidth)

	if got := lipgloss.Width(header); got > narrowWidth {
		t.Fatalf("header width = %d, want at most %d: %q", got, narrowWidth, header)
	}
	if strings.Count(header, "[") != strings.Count(header, "]") {
		t.Fatalf("header has an unclosed badge: %q", header)
	}
	if !strings.HasSuffix(header, "──") {
		t.Fatalf("header should end in separator fill, not a clipped badge: %q", header)
	}
	if strings.Contains(header, "[1/") {
		t.Fatalf("narrow header should drop the tail badge, got %q", header)
	}

	// Same state in a pane with room keeps the position badge.
	wide := withBadges(200, 40)
	if wideHeader := headerAt(wide, wide.layout.playlistWidth); !strings.Contains(wideHeader, "[1/") {
		t.Fatalf("wide header should keep the position badge, got %q", wideHeader)
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
		// Shorter still: shuffle, repeat, and speed share one row.
		{rows: 4, wantKept: []string{"SRC", "VOL", "EQ", "SHF [Off] RPT [Off] SPD [1x]"}, wantGone: []string{"+5"}},
		// Too short for the merged row: the modes go before the other controls.
		{rows: 3, wantKept: []string{"SRC", "VOL", "EQ"}, wantGone: []string{"SHF", "RPT", "SPD"}},
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
	m := newColumnTestModel(86, 24)
	m.configSaver = &recordingSaver{}
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
	saver := &recordingSaver{}
	m := newColumnTestModel(86, 24)
	m.configSaver = saver

	m.toggleSettingsPane()
	if got := saver.saved["hide_settings_pane"]; got != "true" {
		t.Fatalf("saved hide_settings_pane = %q, want %q", got, "true")
	}
	m.toggleSettingsPane()
	if got := saver.saved["hide_settings_pane"]; got != "false" {
		t.Fatalf("saved hide_settings_pane = %q, want %q", got, "false")
	}
}

// TestSettingsPaneOpensByDefault checks the startup state: with no
// hide_settings_pane in the config, the pane is drawn.
func TestSettingsPaneOpensByDefault(t *testing.T) {
	m := newColumnTestModel(86, 24)
	if m.hideSettings {
		t.Fatal("hideSettings defaults to true, want the pane open")
	}
	if !m.layout.twoColumn {
		t.Fatal("want the pane drawn at 80x24 by default")
	}
}

// TestClosedSettingsStacksEverySetting checks that closing the pane moves
// every setting below the seek bar: two aligned rows on a frame under 76
// columns, one row with the volume on the right edge on a wider one.
func TestClosedSettingsStacksEverySetting(t *testing.T) {
	for _, tt := range []struct {
		width, height, rows int
	}{
		{75, 24, 2}, {80, 24, 1}, {81, 24, 1}, {160, 48, 1},
	} {
		t.Run(fmt.Sprintf("%dx%d", tt.width, tt.height), func(t *testing.T) {
			m := newColumnTestModel(tt.width, tt.height)
			m.SetHideSettingsPane(true)
			if m.layout.twoColumn || m.layout.settingsRows != tt.rows {
				t.Fatalf("twoColumn=%v settingsRows=%d, want false, %d", m.layout.twoColumn, m.layout.settingsRows, tt.rows)
			}
			stacked := m.renderSettingsStacked()
			if got := len(strings.Split(stacked, "\n")); got != tt.rows {
				t.Fatalf("stacked rows = %d, want %d", got, tt.rows)
			}
			assertViewFits(t, stacked, m.layout.panelWidth, tt.rows)
			plain := ansi.Strip(stacked)
			lines := strings.Split(plain, "\n")
			col := func(line, label string) int { return lipgloss.Width(line[:strings.Index(line, label)]) }
			want := []focusArea{focusPlaylist, focusProvPill, focusEQ, focusShuffle, focusRepeat, focusSpeed, focusVolume}
			switch {
			case tt.rows == 2:
				if !strings.Contains(plain, "VOL +0dB") || strings.ContainsAny(plain, "█░") || strings.Contains(plain, "1/3") {
					t.Fatalf("two rows want a bare volume readout and no source count:\n%s", plain)
				}
				if col(lines[0], "[") != col(lines[1], "[") {
					t.Fatalf("two rows want the values aligned under SRC and EQ:\n%s", plain)
				}
				want = []focusArea{focusPlaylist, focusProvPill, focusVolume, focusSpeed, focusEQ, focusShuffle, focusRepeat}
			case lipgloss.Width(plain) != m.layout.panelWidth || !strings.HasSuffix(plain, "dB"):
				t.Fatalf("one row wants the volume on the right edge:\n%s", plain)
			case tt.width >= 160 && strings.Contains(plain, "1/3"):
				t.Fatalf("stacked settings never show the source count:\n%s", plain)
			case tt.width >= 160 && !strings.Contains(plain, "█"):
				t.Fatalf("a wide row wants the volume meter:\n%s", plain)
			}
			if got := lipgloss.Width(m.renderPlaylistHeader()); got != m.layout.panelWidth {
				t.Fatalf("playlist header width = %d, want the full panel %d", got, m.layout.panelWidth)
			}
			frame := ansi.Strip(m.View().Content)
			for _, want := range []string{"SRC", "VOL", "EQ", "SHF", "RPT", "SPD"} {
				if !strings.Contains(frame, want) {
					t.Fatalf("closed layout lost %q:\n%s", want, frame)
				}
			}
			if got := m.mainFocusAreas(); !slices.Equal(got, want) {
				t.Fatalf("Tab stops = %v, want %v", got, want)
			}
		})
	}
}

// TestSettingsPaneToggleIgnoredOnNarrowFrames checks that the binding is not
// advertised where there is no pane to open.
func TestSettingsPaneToggleIgnoredOnNarrowFrames(t *testing.T) {
	for _, size := range []struct{ width, height int }{{56, 16}, {40, 10}} {
		t.Run(fmt.Sprintf("%dx%d", size.width, size.height), func(t *testing.T) {
			m := newColumnTestModel(size.width, size.height)
			if strings.Contains(ansi.Strip(m.helpBar(commandModeMain)), "Ctrl+b") {
				t.Fatal("settings-pane binding advertised on a narrow frame")
			}
		})
	}
}

// TestFillSeparator covers the helper the column headers rely on: extend to the width, clip past it, and style the fill so it continues
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
