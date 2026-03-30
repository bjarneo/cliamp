package ui

import "strings"

// renderBarsOutline draws only the top edge of each bar as a horizontal line,
// with empty space below — a minimal line-graph style visualizer.
func (v *Visualizer) renderBarsOutline(bands []float64) string {
	height := v.Rows
	lines := make([]string, height)
	bandCount := len(bands)

	for row := range height {
		var content strings.Builder
		rowBottom := float64(height-1-row) / float64(height)
		rowTop := float64(height-row) / float64(height)

		for i, level := range bands {
			bw := visBandWidth(bandCount, i)
			if level >= rowTop {
				// Fully below the peak — empty inside.
				for range bw {
					content.WriteByte(' ')
				}
			} else if level > rowBottom {
				// This row contains the peak — draw the outline.
				for range bw {
					content.WriteRune('─')
				}
			} else {
				// Above the peak — empty.
				for range bw {
					content.WriteByte(' ')
				}
			}
			if i < bandCount-1 {
				content.WriteByte(' ')
			}
		}
		lines[row] = specStyle(rowBottom).Render(content.String())
	}

	return strings.Join(lines, "\n")
}
