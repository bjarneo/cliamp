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
	fullVisualizerRows int
}

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
		layout.visualizerRows = ui.DefaultVisRows
		layout.fixedRows = 16
	case width >= 56 && height >= 16:
		layout.tier = layoutCompact
		layout.visualizerRows = 3
		layout.fixedRows = 12
	default:
		layout.tier = layoutMinimal
		layout.fixedRows = 7
	}
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
	}
	// The simplified view never draws the hint bar, so its fixedRows budget
	// does not include that row and must not be reduced here.
	if m.hideHelpBar && !simplified {
		layout.fixedRows = max(0, layout.fixedRows-1)
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
			if contentFirst {
				// Keep the normal canvas size cached while visualizer work is paused
				// so modes resume with valid dimensions when the layout returns.
				switch layout.tier {
				case layoutFull:
					rows = ui.DefaultVisRows
				case layoutCompact:
					rows = 3
				default:
					rows = 1
				}
			}
			m.vis.Rows = rows
		}
	}
}
