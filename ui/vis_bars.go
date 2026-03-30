package ui

import "strings"

// renderBars is the default smooth spectrum with fractional Unicode blocks.
func (v *Visualizer) renderBars(bands []float64) string {
	height := v.Rows
	lines := make([]string, height)
	bandCount := len(bands)

	for row := range height {
		var content strings.Builder
		rowBottom := float64(height-1-row) / float64(height)
		rowTop := float64(height-row) / float64(height)

		for i, level := range bands {
			bw := visBandWidth(bandCount, i)
			block := fracBlock(level, rowBottom, rowTop)
			for range bw {
				content.WriteString(block)
			}
			if i < bandCount-1 {
				content.WriteByte(' ')
			}
		}
		lines[row] = specStyle(rowBottom).Render(content.String())
	}

	return strings.Join(lines, "\n")
}
