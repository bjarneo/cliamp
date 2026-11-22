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
// The one-column playback view stacks the same settings below the seek bar.

// eqBandLabels names the ten EQ bands. A band sitting at 0 dB shows its
// frequency instead of a gain, so the row doubles as the band legend.
var eqBandLabels = [10]string{"70", "180", "320", "600", "1k", "3k", "6k", "12k", "14k", "16k"}

// renderBodyRegion renders the body: its header row, then the playlist beside
// the settings pane in the two-column layout, or the full-width body otherwise.
// It always fills the layout's body rows.
func (m Model) renderBodyRegion() string {
	if m.layout.bodyRows <= 0 {
		return ""
	}
	rows := m.layout.listRows()
	if !m.layout.twoColumn {
		header := m.renderPlaylistHeader()
		if rows == 0 {
			return header
		}
		return header + "\n" + ui.FitRect(m.renderMainBody(), m.layout.panelWidth, rows)
	}

	left := columnLines(m.renderPlaylistColumn(), m.layout.playlistWidth, rows)
	right := columnLines(m.renderSettingsPane(rows), m.layout.settingsWidth, rows)

	joined := make([]string, 0, rows+1)
	joined = append(joined, m.renderColumnHeaders())
	for i := range rows {
		joined = append(joined, left[i]+columnGutter+right[i])
	}
	return strings.Join(joined, "\n")
}

// stackedGap separates the items of a stacked settings row.
const stackedGap = " "

// The one-row stacked settings draw the volume meter between these widths
// when the row has room to spare.
const (
	stackedVolumeBarMinWidth = 8
	stackedVolumeBarMaxWidth = 20
)

// renderSettingsStacked renders the settings below the seek bar, on one row
// when the frame is wide enough and on two aligned rows otherwise: source,
// volume, and speed, then EQ, shuffle, repeat, and network.
func (m Model) renderSettingsStacked() string {
	w := m.layout.panelWidth
	if m.layout.settingsRows == 1 {
		return m.renderSettingsOneRow(w)
	}
	label, db := m.volumeReadout()
	return alignSettings(w, [][]string{
		{m.settingsSource(w, false), label + db, m.settingsSpeed(), m.settingsNetworkProgress(w)},
		{m.settingsEQPreset("EQ  "), m.settingsShuffle(), m.settingsRepeat(), m.settingsNetworkSpeed(w)},
	})
}

// renderSettingsOneRow lays out source, EQ, shuffle, repeat, and speed from
// the left and the volume against the right edge. Spare room brings the volume
// meter back and appends the network counters when they fit.
func (m Model) renderSettingsOneRow(w int) string {
	modes := joinSettings(stackedGap, m.settingsEQPreset("EQ "), m.settingsShuffle(), m.settingsRepeat(), m.settingsSpeed())
	left := joinSettings(stackedGap, m.settingsSource(w, false), modes)
	label, db := m.volumeReadout()
	right := label + db
	spare := w - lipgloss.Width(left) - len(stackedGap) - lipgloss.Width(right)
	if barW := min(stackedVolumeBarMaxWidth, spare-1); barW >= stackedVolumeBarMinWidth {
		right = label + m.volumeBar(barW) + db
		spare -= barW + 1
	}
	if net := m.settingsNetwork(w); net != "" && spare > len(stackedGap) {
		left += stackedGap + ansi.Truncate(net, spare-len(stackedGap), "")
	}

	left = ansi.Truncate(left, max(0, w-lipgloss.Width(right)-len(stackedGap)), "")
	pad := max(len(stackedGap), w-lipgloss.Width(left)-lipgloss.Width(right))
	return ansi.Truncate(left+strings.Repeat(" ", pad)+right, w, "")
}

// alignSettings lays rows of settings out in columns, so the labels of each
// column line up from one row to the next.
func alignSettings(w int, rows [][]string) string {
	var widths []int
	for i, row := range rows {
		row = slices.DeleteFunc(row, func(s string) bool { return s == "" })
		rows[i] = row
		for col, cell := range row {
			if col == len(widths) {
				widths = append(widths, 0)
			}
			widths[col] = max(widths[col], lipgloss.Width(cell))
		}
	}
	lines := make([]string, len(rows))
	for i, row := range rows {
		var b strings.Builder
		for col, cell := range row {
			b.WriteString(cell)
			if col < len(row)-1 {
				b.WriteString(strings.Repeat(" ", widths[col]-lipgloss.Width(cell)) + stackedGap)
			}
		}
		lines[i] = ansi.Truncate(b.String(), w, "")
	}
	return strings.Join(lines, "\n")
}

// joinSettings joins the non-empty settings items with sep.
func joinSettings(sep string, items ...string) string {
	items = slices.DeleteFunc(items, func(s string) bool { return s == "" })
	return strings.Join(items, sep)
}

// renderPlaylistColumn renders the body against the playlist column width
// rather than the full frame. The render helpers draw to m.layout.panelWidth,
// so it narrows that width on its own copy of the Model.
func (m Model) renderPlaylistColumn() string {
	m.layout.panelWidth = m.layout.playlistWidth
	return m.renderMainBody()
}

// renderColumnHeaders renders the single header row above the two columns: the
// playlist header on the left and the settings label on the right, each
// separator running to its own column edge so the blank gutter between them
// stays open from the header down. The playlist header renders against its
// column width, so it drops the badges the column cannot hold.
func (m Model) renderColumnHeaders() string {
	m.layout.panelWidth = m.layout.playlistWidth
	return fillSeparator(m.renderPlaylistHeader(), m.layout.playlistWidth) +
		columnGutter +
		fillSeparator(sepHeader("Settings", m.layout.settingsWidth), m.layout.settingsWidth)
}

// settingsPaneMaxRows is the pane's full height: source, volume, EQ preset,
// two band rows, shuffle, repeat, speed, network.
const settingsPaneMaxRows = 9

// paneRow is one settings-pane line together with how readily it is given up
// when the body cannot hold every row. Lower ranks are kept longer.
type paneRow struct {
	line  string
	rank  int
	focus []focusArea // the controls on the row; none for detail/info rows
}

const (
	rankControl = iota // a setting the listener reaches for directly
	rankMode           // shuffle, repeat, and speed, merged onto one row when short
	rankDetail         // the EQ band gains behind the preset name
	rankInfo           // read-only network counters
)

// renderSettingsPane renders the right-hand column, ordered as a signal chain:
// where the audio comes from (SRC), how loud it is and how it is shaped (VOL,
// EQ), how the list plays (SHF, RPT, SPD), and last what the stream is doing
// (NET).
//
// A short body sheds rows by rank rather than by position, so the read-only
// counters and the band detail go before anything the listener can change.
// Ranks are dropped whole: half an EQ curve would read as a rendering bug
// rather than a compromise, so the pane can end up a row shorter than it was
// given. Still short, it puts shuffle, repeat, and speed on one row.
func (m Model) renderSettingsPane(rows int) string {
	panes := m.regionPanes(ui.RegionSettings)
	if len(panes) == 1 && panes[0].Pane == ui.PaneBuiltin {
		// Same shortcut rule as the visualizer band: a lone plugin pane is not
		// the settings column.
		return m.renderSettingsBuiltin(rows)
	}
	heights := ui.SplitWeights(rows, paneWeights(panes))
	var out []string
	for i, pane := range panes {
		block := ""
		switch {
		case pane.Vis != "":
			// A pane can show any visualizer, in either region. In a narrow
			// column the frame is tall instead of wide, so the built-in modes
			// that assume a band look cramped, but they render.
			block = m.renderPaneVis(pane.Vis, heights[i], m.layout.settingsWidth)
		case pane.Pane == ui.PaneBuiltin:
			block = m.renderSettingsBuiltin(heights[i])
		default:
			block = m.renderPluginPane(pane.Pane, heights[i], m.layout.settingsWidth)
		}
		lines := strings.Split(block, "\n")
		for row := 0; row < heights[i]; row++ {
			if row < len(lines) {
				out = append(out, lines[row])
			} else {
				out = append(out, "")
			}
		}
	}
	return strings.Join(out, "\n")
}

func (m Model) renderSettingsBuiltin(rows int) string {
	gap, metadataRows := m.metadataPaneBudget(rows)
	pane := m.settingsPaneRows(rows - gap - metadataRows)
	lines := make([]string, 0, len(pane)+gap+metadataRows)
	for _, r := range pane {
		lines = append(lines, r.line)
	}
	for range gap {
		lines = append(lines, "")
	}
	lines = append(lines, m.renderMetadataPane(metadataRows)...)
	return bodyLines(lines, rows)
}

// settingsPaneRows returns the visible settings rows in display/Tab order,
// without padding. Rendering and focus must use the same row budget.
func (m Model) settingsPaneRows(rows int) []paneRow {
	w := m.layout.settingsWidth

	pane := make([]paneRow, 0, settingsPaneMaxRows)
	add := func(rank int, focus []focusArea, lines ...string) {
		for _, line := range lines {
			if line != "" {
				pane = append(pane, paneRow{line: line, rank: rank, focus: focus})
			}
		}
	}

	add(rankControl, []focusArea{focusProvPill}, m.settingsSource(w, true))
	add(rankControl, []focusArea{focusVolume}, m.settingsVolume(w))
	add(rankControl, []focusArea{focusEQ}, m.settingsEQPreset("EQ  "))
	add(rankDetail, nil, m.settingsEQBands()...)
	add(rankMode, []focusArea{focusShuffle}, m.settingsShuffle())
	add(rankMode, []focusArea{focusRepeat}, m.settingsRepeat())
	add(rankMode, []focusArea{focusSpeed}, m.settingsSpeed())
	add(rankInfo, nil, m.settingsNetwork(w))

	for rank := rankInfo; rank > rankMode && len(pane) > rows; rank-- {
		pane = slices.DeleteFunc(pane, func(r paneRow) bool { return r.rank == rank })
	}
	if first := slices.IndexFunc(pane, func(r paneRow) bool { return r.rank == rankMode }); first >= 0 && len(pane) > rows {
		merged := paneRow{rank: rankMode}
		var lines []string
		end := first
		for ; end < len(pane) && pane[end].rank == rankMode; end++ {
			lines = append(lines, pane[end].line)
			merged.focus = append(merged.focus, pane[end].focus...)
		}
		merged.line = joinSettings(" ", lines...)
		pane = slices.Replace(pane, first, end, merged)
	}

	return pane[:min(len(pane), max(0, rows))]
}

// settingsNetwork renders the live stream counters, or "" for local playback
// where there is nothing to report.
func (m Model) settingsNetwork(w int) string {
	return m.settingsNetworkParts(w, m.networkStatsText())
}

// settingsNetworkProgress renders the NET label and the byte counters.
func (m Model) settingsNetworkProgress(w int) string {
	return m.settingsNetworkParts(w, m.networkProgressText())
}

// settingsNetworkSpeed renders the live download speed for the stacked second
// row, or "" when the stream is idle or already complete.
func (m Model) settingsNetworkSpeed(w int) string {
	speed := m.networkSpeedText()
	if speed == "" {
		return ""
	}
	return dimStyle.Render(truncate(speed, max(1, w)))
}

func (m Model) settingsNetworkParts(w int, stats string) string {
	if stats == "" {
		return ""
	}
	label := labelStyle.Render("NET ")
	return label + dimStyle.Render(truncate(stats, max(1, w-lipgloss.Width(label))))
}

// settingsEQPreset renders the "EQ [Preset]" row behind the given label.
func (m Model) settingsEQPreset(label string) string {
	label = labelStyle.Render(label)
	if m.focus == focusEQ {
		label = activeToggle.Render("EQ ▸ ")
		// The band rows may be shed for space; editing must still show which
		// band changes and its gain before the lower-priority preset name.
		bands := m.player.EQBands()
		label += eqActiveStyle.Render(fmt.Sprintf("%s %+.0fdB ", eqBandLabels[m.eqCursor], bands[m.eqCursor]))
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
	label, db := m.volumeReadout()
	return label + m.volumeBar(max(6, w-lipgloss.Width(label)-1-lipgloss.Width(db))) + db
}

// volumeBar renders the volume meter barW cells wide, followed by a space.
func (m Model) volumeBar(barW int) string {
	volMin := m.player.VolumeMin()
	frac := max(0, min(1, (m.player.Volume()-volMin)/(6-volMin)))
	filled := int(frac * float64(barW))
	return volBarStyle.Render(strings.Repeat("█", filled)) +
		dimStyle.Render(strings.Repeat("░", barW-filled)+" ")
}

// volumeReadout returns the VOL label and the gain, with the mono marker.
func (m Model) volumeReadout() (label, db string) {
	label = labelStyle.Render("VOL ")
	if m.focus == focusVolume {
		label = activeToggle.Render("VOL ▸ ")
	}
	db = dimStyle.Render(fmt.Sprintf("%+.0fdB", m.player.Volume()))
	if m.player.Mono() {
		db += " " + activeToggle.Render("[M]")
	}
	return label, db
}

// settingsSource renders the active provider, with its position among the
// providers when count is set, or "" when there is only one source to pick
// from and the row would say nothing.
func (m Model) settingsSource(w int, count bool) string {
	if len(m.providers) <= 1 {
		return ""
	}
	label := labelStyle.Render("SRC ")
	nameStyle := trackStyle
	if m.focus == focusProvPill {
		label = activeToggle.Render("SRC ▸ ")
		nameStyle = activeToggle
	}
	position := ""
	if count {
		position = fmt.Sprintf(" %d/%d", m.provPillIdx+1, len(m.providers))
	}
	name := truncate(m.providers[m.provPillIdx].Name, max(1, w-lipgloss.Width(label)-2-lipgloss.Width(position)))
	return label + dimStyle.Render("[") + nameStyle.Render(name) + dimStyle.Render("]") + dimStyle.Render(position)
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
	return settingsRow("SHF ", value, m.playlist.Shuffled(), m.focus == focusShuffle)
}

func (m Model) settingsRepeat() string {
	mode := m.playlist.Repeat()
	return settingsRow("RPT ", mode.String(), mode != 0, m.focus == focusRepeat)
}

// settingsRow renders one "LBL [value]" pane row, highlighting the value when
// it is away from its default and marking the focused control.
func settingsRow(label, value string, active, focused bool) string {
	if focused {
		return activeToggle.Render(label+"▸ ") + activeToggle.Render("["+value+"]")
	}
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
