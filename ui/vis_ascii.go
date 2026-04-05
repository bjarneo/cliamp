package ui

import "strings"

// shadeBlock maps fractional fill within a row to Unicode shade characters: █ ▓ ▒ ░
func shadeBlock(level, rowBottom, rowTop float64) string {
	if level >= rowTop {
		return "█"
	}
	if level > rowBottom {
		frac := (level - rowBottom) / (rowTop - rowBottom)
		switch {
		case frac >= 0.75:
			return "▓"
		case frac >= 0.50:
			return "▒"
		case frac >= 0.25:
			return "░"
		}
	}
	return " "
}

// renderAscii draws thin single-character columns with shade blocks (█ ▓ ▒ ░),
// using the same dense 1-wide/1-gap layout as ClassicPeak.
func (v *Visualizer) renderAscii(bands []float64) string {
	height := v.Rows
	activeCols := classicPeakColsForWidth(PanelWidth)
	cols := resampleBandsLinear(bands, activeCols)
	lines := make([]string, height)

	for row := range height {
		var content strings.Builder
		rowBottom := float64(height-1-row) / float64(height)
		rowTop := float64(height-row) / float64(height)

		for i, level := range cols {
			content.WriteString(shadeBlock(level, rowBottom, rowTop))
			if i < len(cols)-1 {
				content.WriteByte(' ')
			}
		}
		lines[row] = specStyle(rowBottom).Render(content.String())
	}

	return strings.Join(lines, "\n")
}
