package model

import (
	"charm.land/lipgloss/v2"

	"github.com/bjarneo/cliamp/ui"
)

// frameLayout is the row and column budget of one frame. From top to bottom
// the frame stacks: title, track info, time status, spectrum, seek bar,
// stacked settings, gap, body, transient, help bar. Every section has a fixed
// height and the body takes the rest, so the transient and the help bar stay
// at the bottom.
type frameLayout struct {
	frameWidth int // the terminal width the frame fills
	panelWidth int // the frame width inside the horizontal padding
	paddingH   int
	paddingV   int
	small      bool

	height       int // the frame height inside the vertical padding
	titleRows    int
	spectrumRows int
	settingsRows int // stacked settings rows below the seek bar
	gapRows      int
	helpRows     int
	bodyRows     int // the body header plus its list rows

	// twoColumn splits the body into the playlist (left) and the settings
	// pane (right); the two widths are zero when it is off.
	twoColumn     bool
	playlistWidth int
	settingsWidth int

	fullVisualizerRows int
}

const (
	defaultFrameBaseWidth  = 150
	defaultFrameBaseHeight = 24
	// titleMinHeight, spectrumMinHeight, and gapMinHeight are the frame heights
	// that afford the title, the full vis_rows spectrum, and the gap row.
	// Below titleMinHeight the spectrum is hidden, below spectrumMinHeight it
	// is cut to compactSpectrumRows, and below gapMinHeight the gap row is
	// omitted.
	titleMinHeight      = 20
	spectrumMinHeight   = 25
	gapMinHeight        = 25
	compactSpectrumRows = 3
	// One row each for the track info, the time status, the seek bar and the
	// transient.
	alwaysShownRows = 4

	twoColumnMinWidth = 80
	// settingsOneRowMinWidth is the narrowest frame that fits every stacked
	// setting on one row: "SRC [cliamp radio]", "EQ [Custom]", "SHF [Off]",
	// "RPT [Off]", "SPD [1x]", and "VOL -30dB", two cells apart, plus the
	// focus marker.
	settingsOneRowMinWidth = 76
	// stackedSettingsMinHeight is the shortest frame that keeps the stacked
	// settings.
	stackedSettingsMinHeight = 14
	settingsColumnWidth      = 30
	// columnGutter is the blank channel between the two columns. Its width is
	// the declared one, not len(): a non-ASCII gutter would make those differ.
	columnGutterWidth = 5
	columnGutter      = "     "
)

func (l frameLayout) tooSmall() bool {
	return l.small
}

// settingsOneRow reports whether the stacked settings fit on one row.
func (l frameLayout) settingsOneRow() bool { return l.panelWidth >= settingsOneRowMinWidth }

// fixedRows counts the rows of every section but the spectrum and the body.
func (l frameLayout) fixedRows() int {
	return l.titleRows + alwaysShownRows + l.settingsRows + l.gapRows + l.helpRows
}

// listRows is the body height below its header row.
func (l frameLayout) listRows() int {
	return max(0, l.bodyRows-1)
}

// frameStyle pads the frame and gives it the terminal width.
func (l frameLayout) frameStyle() lipgloss.Style {
	return lipgloss.NewStyle().Padding(l.paddingV, l.paddingH).Width(l.frameWidth)
}

// recomputeLayout derives the frame layout from the terminal size and the
// active view.
func (m *Model) recomputeLayout() {
	width, height := m.width, m.height
	if width <= 0 {
		width = 80
	}
	if height <= 0 {
		height = 24
	}

	paddingH, paddingV := 0, 0
	if m.paddingSet {
		paddingH, paddingV = m.paddingH, m.paddingV
	}
	paddingH = min(paddingH, max(0, (width-1)/2))
	paddingV = min(paddingV, max(0, (height-1)/2))

	baseW := m.frameBaseWidth
	if baseW <= 0 {
		baseW = defaultFrameBaseWidth
	}
	baseH := m.frameBaseHeight
	if baseH <= 0 {
		baseH = defaultFrameBaseHeight
	}

	visRows := m.visualizerRowsSetting()
	if !m.heightExpanded {
		width = min(width, baseW)
	}
	l := frameLayout{
		frameWidth:         width,
		panelWidth:         max(1, width-2*paddingH),
		paddingH:           paddingH,
		paddingV:           paddingV,
		small:              width < 40 || height < 10,
		height:             max(0, height-2*paddingV),
		fullVisualizerRows: max(1, height-6-2*paddingV),
	}
	if !m.heightExpanded {
		l.height = min(l.height, baseH+visRows)
	}
	if l.height >= titleMinHeight {
		l.titleRows = 1
	}
	if l.height >= gapMinHeight {
		l.gapRows = 1
	}
	if !m.hideHelpBar {
		l.helpRows = 1
	}

	// The playback view shows the spectrum and the settings. The list-heavy
	// views give those rows to their list, and the simplified view has neither.
	simplified := m.usesSimplifiedLayout()
	if !simplified && !m.usesContentFirstLayout() {
		switch {
		case l.panelWidth >= twoColumnMinWidth && !m.hideSettings:
			l.twoColumn = true
			l.settingsWidth = settingsColumnWidth
			l.playlistWidth = l.panelWidth - settingsColumnWidth - columnGutterWidth
		case l.height < stackedSettingsMinHeight:
		case l.settingsOneRow():
			l.settingsRows = 1
		default:
			l.settingsRows = 2
		}
		switch {
		case m.visualizerDisabled() || l.height < titleMinHeight:
		case l.height < spectrumMinHeight:
			l.spectrumRows = min(compactSpectrumRows, visRows)
		default:
			l.spectrumRows = visRows
		}
		// The spectrum never takes more rows than it leaves to the body.
		l.spectrumRows = min(l.spectrumRows, max(0, l.height-l.fixedRows())/2)
	}

	if !simplified {
		l.bodyRows = max(0, l.height-l.fixedRows()-l.spectrumRows)
	}
	m.plVisible = l.listRows()
	m.layout = l

	if m.vis != nil {
		m.vis.Cols = l.panelWidth
		if m.width <= 0 && !m.headless {
			// Bubbletea draws a frame before the first WindowSizeMsg. The
			// visualizer stays unsized until then, so a mode never starts at
			// the placeholder size. Headless mode keeps it for spectrum.get.
			m.vis.Cols = 0
		}
		switch {
		case m.simplified:
			m.vis.Rows = 0
		case m.fullVis:
			m.vis.Rows = l.fullVisualizerRows
		case l.spectrumRows > 0:
			m.vis.Rows = l.spectrumRows
		default:
			// Keep a valid canvas while the spectrum is hidden, so modes resume
			// with real dimensions when it returns.
			m.vis.Rows = visRows
		}
	}
}

// visualizerRowsSetting returns the configured visualizer height, falling back
// to the built-in default when unset.
func (m *Model) visualizerRowsSetting() int {
	if m.visRows > 0 {
		return m.visRows
	}
	return ui.DefaultVisRows
}
