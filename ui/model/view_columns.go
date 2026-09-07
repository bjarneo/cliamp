package model

import (
	"fmt"
	"slices"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/bjarneo/cliamp/ui"
)

// The two-column body puts the playlist on the left and the focusable playback
// settings on the right, separated by blank space rather than a drawn rule.
// The stacked EQ/volume, source, and bottom status rows move into the settings
// pane, so the rows they used go to the playlist.

// eqBandLabels names the ten EQ bands. A band sitting at 0 dB shows its
// frequency instead of a gain, so the row doubles as the band legend.
var eqBandLabels = [10]string{"70", "180", "320", "600", "1k", "3k", "6k", "12k", "14k", "16k"}

// renderBodyRegion renders the playlist region: the playlist beside the
// settings pane in the two-column layout, or the full-width body otherwise.
func (m Model) renderBodyRegion() string {
	if !m.layout.twoColumn {
		return ui.FitRect(m.renderMainBody(), m.layout.panelWidth, m.layout.bodyRows)
	}

	rows := m.effectivePlaylistVisible()
	if rows <= 0 {
		return ""
	}

	left := columnLines(m.renderPlaylistColumn(), m.layout.playlistWidth, rows)
	right := columnLines(m.renderSettingsPane(rows), m.layout.settingsWidth, rows)

	joined := make([]string, rows)
	for i := range joined {
		joined[i] = left[i] + columnGutter + right[i]
	}
	return strings.Join(joined, "\n")
}

// renderPlaylistColumn renders the body against the playlist column width
// rather than the full frame. The package renders against the ui.PanelWidth
// global, so the width is handed over by narrowing it for the call; the
// restore is deferred because overlay bodies run arbitrary code.
func (m Model) renderPlaylistColumn() string {
	defer ui.WithPanelWidth(m.layout.playlistWidth)()
	return m.renderMainBody()
}

// renderColumnHeaders renders the single header row above the two columns: the
// playlist header on the left and the settings label on the right, each
// separator running to its own column edge so the blank gutter between them
// stays open from the header down.
func (m Model) renderColumnHeaders() string {
	// Both headers are fitted to their own column below, so neither needs
	// ui.PanelWidth narrowed first: the playlist header never reads it in this
	// layout (an overlay or provider focus would have turned the split off),
	// and the settings separator is re-fitted either way.
	return fillSeparator(m.renderPlaylistHeader(), m.layout.playlistWidth) +
		columnGutter +
		fillSeparator(sepHeader("Settings"), m.layout.settingsWidth)
}

// settingsPaneMaxRows is the pane's full height: source, volume, EQ preset,
// two band rows, shuffle, repeat, speed, network.
const settingsPaneMaxRows = 9

// paneRow is one settings-pane line together with how readily it is given up
// when the body cannot hold every row. Lower ranks are kept longer.
type paneRow struct {
	line string
	rank int
}

const (
	rankControl = iota // a setting the listener reaches for directly
	rankMode           // shuffle and repeat
	rankDetail         // the EQ band gains behind the preset name
	rankInfo           // read-only network counters
)

// renderSettingsPane renders the right-hand column, ordered as a signal chain:
// where the audio comes from (SRC), how loud it is and how it is shaped (VOL,
// EQ), how the list plays (SHF, RPT, SPD), and last what the stream is doing
// (NET).
//
// A short body — a tall vis_rows can leave it only a few rows — sheds rows by
// rank rather than by position, so the read-only counters and the band detail
// go before anything the listener can change. Ranks are dropped whole: half an
// EQ curve, or shuffle without repeat, would read as a rendering bug rather
// than a compromise, so the pane can end up a row shorter than it was given.
func (m Model) renderSettingsPane(rows int) string {
	w := m.layout.settingsWidth

	pane := make([]paneRow, 0, settingsPaneMaxRows)
	add := func(rank int, lines ...string) {
		for _, line := range lines {
			if line != "" {
				pane = append(pane, paneRow{line: line, rank: rank})
			}
		}
	}

	add(rankControl, m.settingsSource(w))
	add(rankControl, m.settingsVolume(w))
	add(rankControl, m.settingsEQPreset())
	add(rankDetail, m.settingsEQBands()...)
	add(rankMode, m.settingsShuffle(), m.settingsRepeat())
	add(rankControl, m.settingsSpeed())
	add(rankInfo, m.settingsNetwork(w))

	for rank := rankInfo; rank > rankControl && len(pane) > rows; rank-- {
		pane = slices.DeleteFunc(pane, func(r paneRow) bool { return r.rank == rank })
	}

	lines := make([]string, 0, len(pane))
	for _, r := range pane {
		lines = append(lines, r.line)
	}
	return bodyLines(lines, rows)
}

// settingsNetwork renders the live stream counters, or "" for local playback
// where there is nothing to report.
func (m Model) settingsNetwork(w int) string {
	stats := m.networkStatsText()
	if stats == "" {
		return ""
	}
	label := labelStyle.Render("NET ")
	return label + dimStyle.Render(truncate(stats, max(1, w-lipgloss.Width(label))))
}

// settingsEQPreset renders the "EQ [Preset]" row.
func (m Model) settingsEQPreset() string {
	label := labelStyle.Render("EQ  ")
	if m.focus == focusEQ {
		label = activeToggle.Render("EQ ▸ ")
	}
	return label + dimStyle.Render("[") + activeToggle.Render(m.EQPresetName()) + dimStyle.Render("]")
}

// The band rows are indented so their cells line up just under the preset
// bracket on the row above: each cell is right-aligned in eqBandCellWidth, so
// a two-character gain sits under the "[" and a three-character one starts a
// column to its left.
const (
	eqBandIndent    = "   "
	eqBandCellWidth = 3
	eqBandRows      = 2
)

// settingsEQBands renders the ten bands as two rows of five, each cell padded
// to a fixed width so the second row lines up under the first. The full-width
// layout fits all ten on one row; the column does not.
func (m Model) settingsEQBands() []string {
	perRow := len(eqBandLabels) / eqBandRows
	bands := m.player.EQBands()
	rows := make([]string, 0, eqBandRows)
	for start := 0; start < len(eqBandLabels); start += perRow {
		cells := make([]string, 0, perRow)
		for i := start; i < start+perRow; i++ {
			label := eqBandLabels[i]
			style := eqInactiveStyle
			if bands[i] != 0 {
				label = fmt.Sprintf("%+.0f", bands[i])
			}
			if m.focus == focusEQ && i == m.eqCursor {
				style = eqActiveStyle
			}
			cells = append(cells, style.Render(fmt.Sprintf("%*s", eqBandCellWidth, label)))
		}
		rows = append(rows, eqBandIndent+strings.Join(cells, " "))
	}
	return rows
}

// settingsVolume renders the volume bar sized to the column width.
func (m Model) settingsVolume(w int) string {
	vol := m.player.Volume()
	volMin := m.player.VolumeMin()
	frac := max(0, min(1, (vol-volMin)/(6-volMin)))

	dbStr := fmt.Sprintf(" %+.0fdB", vol)
	mono := ""
	if m.player.Mono() {
		mono = " " + activeToggle.Render("[M]")
	}

	label := labelStyle.Render("VOL ")
	barW := max(6, w-lipgloss.Width(label)-lipgloss.Width(dbStr)-lipgloss.Width(mono))
	filled := int(frac * float64(barW))

	return label + volBarStyle.Render(strings.Repeat("█", filled)) +
		dimStyle.Render(strings.Repeat("░", barW-filled)) + dimStyle.Render(dbStr) + mono
}

// settingsSource renders the active provider, or "" when there is only one
// source to pick from and the row would say nothing.
func (m Model) settingsSource(w int) string {
	if len(m.providers) <= 1 {
		return ""
	}
	label := labelStyle.Render("SRC ")
	nameStyle := trackStyle
	if m.focus == focusProvPill {
		label = activeToggle.Render("SRC ▸ ")
		nameStyle = activeToggle
	}
	count := fmt.Sprintf(" %d/%d", m.provPillIdx+1, len(m.providers))
	name := truncate(m.providers[m.provPillIdx].Name, max(1, w-lipgloss.Width(label)-2-lipgloss.Width(count)))
	return label + dimStyle.Render("[") + nameStyle.Render(name) + dimStyle.Render("]") + dimStyle.Render(count)
}

// settingsSpeed renders the playback-speed row.
func (m Model) settingsSpeed() string {
	speed := m.player.Speed()
	if speed == 0 {
		speed = 1.0
	}
	val := fmt.Sprintf("%.2gx", speed)

	label := labelStyle.Render("SPD ")
	value := dimStyle.Render("[") + trackStyle.Render(val) + dimStyle.Render("]")
	if m.focus == focusSpeed {
		label = activeToggle.Render("SPD ▸ ")
		value = activeToggle.Render("[" + val + "]")
	} else if speed != 1.0 {
		value = activeToggle.Render("[" + val + "]")
	}
	return label + value
}

// settingsShuffle and settingsRepeat render the playlist modes. They live in
// the pane rather than the playlist header here: the header has to fit inside
// the narrower playlist column, and these are settings like the rows above
// them, not counts describing the list.
func (m Model) settingsShuffle() string {
	value := "Off"
	if m.playlist.Shuffled() {
		value = "On"
	}
	return settingsRow("SHF ", value, m.playlist.Shuffled())
}

func (m Model) settingsRepeat() string {
	mode := m.playlist.Repeat()
	return settingsRow("RPT ", mode.String(), mode != 0)
}

// settingsRow renders one "LBL [value]" pane row, highlighting the value when
// it is away from its default.
func settingsRow(label, value string, active bool) string {
	if active {
		return labelStyle.Render(label) + activeToggle.Render("["+value+"]")
	}
	return labelStyle.Render(label) + dimStyle.Render("[") + trackStyle.Render(value) + dimStyle.Render("]")
}

// columnLines clips each line of a rendered block to w cells and pads it back
// out to exactly w, then pads the block to rows, so the gutter beside it stays
// on the same screen columns from the first row to the last.
func columnLines(block string, w, rows int) []string {
	lines := strings.Split(block, "\n")
	if len(lines) > rows {
		lines = lines[:rows]
	}
	for len(lines) < rows {
		lines = append(lines, "")
	}
	for i, line := range lines {
		line = ansi.Truncate(line, w, "")
		if pad := w - lipgloss.Width(line); pad > 0 {
			line += strings.Repeat(" ", pad)
		}
		lines[i] = line
	}
	return lines
}
