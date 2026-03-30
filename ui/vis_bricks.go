package ui

import "strings"

// renderBricks draws solid block columns with visible gaps between rows and bands.
// Uses half-height blocks (▄) so each brick is half a terminal row, with blank
// gaps between them, keeping total height equal to the bars visualizer.
func (v *Visualizer) renderBricks(bands []float64) string {
	height := v.Rows
	lines := make([]string, height)
	bandCount := len(bands)

	for row := range height {
		var content strings.Builder
		rowThreshold := float64(height-1-row) / float64(height)

		for i, level := range bands {
			bw := visBandWidth(bandCount, i)
			if level > rowThreshold {
				for range bw {
					content.WriteString("▄")
				}
			} else {
				for range bw {
					content.WriteByte(' ')
				}
			}
			if i < bandCount-1 {
				content.WriteByte(' ')
			}
		}
		lines[row] = specStyle(rowThreshold).Render(content.String())
	}

	return strings.Join(lines, "\n")
}
