package model

import "github.com/bjarneo/cliamp/ui"

type layoutTier int

const (
	layoutTooSmall layoutTier = iota
	layoutMinimal
	layoutCompact
	layoutFull
)

type frameLayout struct {
	tier               layoutTier
	frameWidth         int
	panelWidth         int
	paddingH           int
	paddingV           int
	fixedRows          int
	footerRows         int
	bodyRows           int
	visualizerRows     int
	baseVisualizerRows int
	fullVisualizerRows int
	// twoColumn splits the body region into the playlist (left) and the
	// settings pane (right); the two widths are zero when it is off.
	twoColumn     bool
	playlistWidth int
	settingsWidth int
	// closedSettings is the same full-tier playback screen with the pane shut:
	// source and volume share one row and the EQ, speed, and download readouts
	// are not drawn at all.
	closedSettings bool
	// minimalControls adds the compact tier's source and EQ/volume rows to the
	// minimal tier once the terminal has the height to spare for them.
	minimalControls bool
}

// minimalControlRows is what the minimal tier adds when it can afford the
// compact tier's controls: one row for EQ and volume, one for the source.
const minimalControlRows = 2

// minimalBaseRows is the minimal tier's chrome: track line, time, seek bar,
// playlist header, hint bar, and status line. Unlike the other tiers it has no
// spacer above the footer, since at this height every row is a track.
const minimalBaseRows = 6

// chromeRowsFreed is how many stacked chrome rows the current layout does not
// draw, and therefore hands to the playlist.
func (l frameLayout) chromeRowsFreed() int {
	switch {
	case l.twoColumn:
		return twoColumnChromeRows
	case l.closedSettings:
		return closedSettingsChromeRows
	default:
		return 0
	}
}

// Two-column body geometry. The columns are separated by blank space rather
// than a rule, so the gutter has to be wide enough to read as a break on its
// own. The settings pane is clamped so it never crowds out the playlist, and
// the split is abandoned entirely when the playlist would end up narrower than
// playlistMinWidth.
const (
	// columnGutter is the blank channel between the two columns. It runs
	// unbroken down the body, which is what separates them. Its width is the
	// declared one, not len(): a non-ASCII gutter would make those differ.
	columnGutterWidth = 5
	columnGutter      = "     "
	settingsMinWidth  = 22
	settingsMaxWidth  = 30
	playlistMinWidth  = 40
	// twoColumnChromeRows counts the stacked rows the two-column body does not
	// draw: the EQ/volume row, the source row, and the status line, which all
	// move into the settings pane, plus the blank spacer above the hint bar,
	// which the pane's own blank tail makes redundant.
	twoColumnChromeRows = 4
	// closedSettingsChromeRows counts what the closed-pane layout drops
	// instead: the EQ row (source and volume share one row) and the status
	// line carrying speed and the download counters.
	closedSettingsChromeRows = 2
)

// Chrome heights are counted as "everything but the visualizer" plus the
// visualizer, so changing a visualizer height cannot leave the row budget
// disagreeing with what is drawn.
const (
	// fullBaseRows is the full tier's chrome without the visualizer: title,
	// track line, time, a blank, the seek bar, the EQ/volume row, the source
	// row, the playlist header, a blank, the hint bar, and the status line.
	fullBaseRows = 11
	// compactBaseRows is the same count for the compact tier, which drops the
	// blank spacers and uses one-line controls.
	compactBaseRows = 9
	// compactVisRows is the compact tier's fixed visualizer height.
	compactVisRows = 5
)

// fullChromeRows is the full tier's chrome height at the default visualizer
// size, the baseline the configurable vis_rows is measured against.
func fullChromeRows() int { return fullBaseRows + ui.DefaultVisRows }

func (l frameLayout) tooSmall() bool {
	return l.tier == layoutTooSmall
}

// recomputeLayout picks the layout tier for the current terminal size and
// derives the row budget from it: fixed chrome, the visualizer, and whatever
// is left for the body.
func (m *Model) recomputeLayout() {
	width, height := m.width, m.height
	if width <= 0 {
		width = 80
	}
	if height <= 0 {
		height = 24
	}

	paddingH := min(ui.PaddingH, max(0, (width-1)/2))
	paddingV := min(ui.VerticalPadding(), max(0, (height-1)/2))

	layout := frameLayout{
		frameWidth: width,
		panelWidth: max(1, width-2*paddingH),
		paddingH:   paddingH,
		paddingV:   paddingV,
		footerRows: 1,
	}
	switch {
	case width < 40 || height < 10:
		layout.tier = layoutTooSmall
	case width >= 80 && height >= 24:
		layout.tier = layoutFull
		// fixedRows counts the default visualizer height, so extra rows come
		// straight out of the playlist below.
		rows := m.visualizerRowsSetting()
		bodyAtDefault := height - 2*paddingV - fullChromeRows() - layout.footerRows
		if extra := rows - ui.DefaultVisRows; extra > 0 {
			rows = ui.DefaultVisRows + min(extra, max(0, bodyAtDefault-1))
		}
		layout.visualizerRows = rows
		layout.fixedRows = fullBaseRows + rows
	case width >= 56 && height >= 16:
		layout.tier = layoutCompact
		layout.visualizerRows = compactVisRows
		layout.fixedRows = compactBaseRows + compactVisRows
	default:
		layout.tier = layoutMinimal
		layout.fixedRows = minimalBaseRows
	}
	layout.baseVisualizerRows = layout.visualizerRows
	contentFirst := m.usesContentFirstLayout()
	simplified := m.usesSimplifiedLayout()
	if contentFirst {
		layout.visualizerRows = 0
		if layout.tier == layoutMinimal {
			layout.fixedRows = 6
		} else {
			layout.fixedRows = 7
		}
	} else if simplified {
		layout.visualizerRows = 0
		layout.fixedRows = 3
	} else if m.visualizerDisabled() {
		layout.visualizerRows = 0
		if layout.tier == layoutFull {
			layout.fixedRows = 10
		} else if layout.tier == layoutCompact {
			layout.fixedRows = 9
		}
	}
	// The minimal tier normally drops the controls to keep a row for tracks.
	// It carries the compact tier's two rows whenever they fit with at least
	// one track row left, so hiding the hint bar buys them a row sooner: 11
	// rows with it hidden, 12 with it shown.
	if layout.tier == layoutMinimal && !contentFirst && !simplified {
		chrome := layout.fixedRows
		if m.hideHelpBar {
			chrome--
		}
		if spare := height - 2*paddingV - layout.footerRows - chrome; spare >= minimalControlRows+1 {
			layout.minimalControls = true
			layout.fixedRows += minimalControlRows
		}
	}
	// The settings pane, open or closed, belongs to the full-tier playback
	// screen only: the denser tiers and the list-focused layouts have no room
	// for a column and draw their own controls.
	if layout.tier == layoutFull && !contentFirst && !simplified && m.activeScreen() == screenMain {
		if m.hideSettings {
			layout.closedSettings = true
		} else {
			settingsWidth := min(settingsMaxWidth, max(settingsMinWidth, layout.panelWidth/3))
			if playlistWidth := layout.panelWidth - columnGutterWidth - settingsWidth; playlistWidth >= playlistMinWidth {
				layout.twoColumn = true
				layout.playlistWidth = playlistWidth
				layout.settingsWidth = settingsWidth
			}
		}
	}
	// Whatever the chrome gives up goes to the playlist, both as budget and as
	// a higher cap so the reclaimed rows show tracks instead of blank space.
	layout.fixedRows = max(0, layout.fixedRows-layout.chromeRowsFreed())
	// The simplified view never draws the hint bar, so its fixedRows budget
	// does not include that row and must not be reduced here.
	if m.hideHelpBar && !simplified {
		layout.fixedRows = max(0, layout.fixedRows-1)
	}
	if layout.twoColumn && m.showMetadata && !m.visualizerDisabled() {
		// Opening details can borrow visualizer rows, never hide direct settings.
		// The configured height stays intact and returns when details close.
		bodyRows := height - 2*paddingV - layout.fixedRows - layout.footerRows
		needed := 5 + metadataPaneMaxRows
		if len(m.providers) > 1 {
			needed++
		}
		freed := min(max(0, needed-bodyRows), max(0, layout.visualizerRows-1))
		layout.visualizerRows -= freed
		layout.fixedRows -= freed
	}

	layout.fullVisualizerRows = max(1, height-6-2*paddingV)
	if !layout.tooSmall() {
		layout.bodyRows = max(1, height-2*paddingV-layout.fixedRows-layout.footerRows)
		if simplified {
			m.plVisible = 0
		} else {
			limit := maxPlVisible
			if m.heightExpanded {
				limit = layout.bodyRows
			} else if contentFirst {
				limit = maxPlExpandVisible
			} else if freed := layout.chromeRowsFreed(); freed > 0 {
				limit = maxPlVisible + freed
			}
			m.plVisible = min(limit, layout.bodyRows)
		}
	}

	m.layout = layout
	ui.FrameStyle = ui.FrameStyle.Padding(paddingV, paddingH).Width(width)
	ui.PanelWidth = layout.panelWidth
	if m.vis != nil {
		m.vis.Cols = layout.panelWidth
		if m.simplified {
			m.vis.Rows = 0
		} else if m.fullVis {
			m.vis.Rows = layout.fullVisualizerRows
		} else {
			rows := layout.visualizerRows
			if contentFirst || m.visualizerDisabled() {
				// Keep the normal canvas size cached while visualizer work is paused
				// so modes resume with valid dimensions when the layout returns.
				rows = max(1, layout.baseVisualizerRows)
			}
			m.vis.Rows = rows
		}
	}
}

// visualizerRowsSetting returns the configured visualizer height at the full
// layout tier, falling back to the built-in default when unset.
func (m *Model) visualizerRowsSetting() int {
	if m.visRows > 0 {
		return m.visRows
	}
	return ui.DefaultVisRows
}
