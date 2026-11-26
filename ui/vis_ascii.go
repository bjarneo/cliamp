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
	width := v.columns()
	activeCols := classicPeakColsForWidth(width)
	v.interpColsBuf = resampleBandsLinearInto(v.interpColsBuf, bands, activeCols)
	cols := v.interpColsBuf

	var sb strings.Builder
	sb.Grow(height * (width*3 + 24))

	for row := range height {
		if row > 0 {
			sb.WriteByte('\n')
		}
		rowBottom := float64(height-1-row) / float64(height)
		rowTop := float64(height-row) / float64(height)
		style := specANSI[specTag(rowBottom)]
		if style.prefix != "" {
			sb.WriteString(style.prefix)
		}

		for i, level := range cols {
			sb.WriteString(shadeBlock(level, rowBottom, rowTop))
			if i < len(cols)-1 {
				sb.WriteByte(' ')
			}
		}
		if style.prefix != "" {
			sb.WriteString(style.suffix)
		}
	}

	return sb.String()
}
