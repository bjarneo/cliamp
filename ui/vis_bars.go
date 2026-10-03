package ui

import "strings"

// renderBars is the default smooth spectrum with fractional Unicode blocks.
func (v *Visualizer) renderBars(bands []float64) string {
	height := v.Rows
	lines := make([]string, height)
	bandCount := len(bands)
	width := v.columns()

	for row := range height {
		var content strings.Builder
		rowBottom := float64(height-1-row) / float64(height)
		rowTop := float64(height-row) / float64(height)

		for i, level := range bands {
			bw := visBandWidth(bandCount, i, width)
			block := fracBlock(level, rowBottom, rowTop)
			for range bw {
				content.WriteString(block)
			}
			if bandGapAfter(bandCount, i, width) {
				content.WriteByte(' ')
			}
		}
		lines[row] = specWrap(rowBottom, content.String())
	}

	return strings.Join(lines, "\n")
}
