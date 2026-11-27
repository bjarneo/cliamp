package ui

import "strings"

// renderBars is the default smooth spectrum with fractional Unicode blocks.
func (v *Visualizer) renderBars(bands []float64) string {
	height := v.Rows
	bandCount := len(bands)
	width := v.columns()

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

		for i, level := range bands {
			bw := visBandWidth(bandCount, i, width)
			block := fracBlock(level, rowBottom, rowTop)
			for range bw {
				sb.WriteString(block)
			}
			if bandGapAfter(bandCount, i, width) {
				sb.WriteByte(' ')
			}
		}
		if style.prefix != "" {
			sb.WriteString(style.suffix)
		}
	}

	return sb.String()
}
