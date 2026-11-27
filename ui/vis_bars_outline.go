package ui

import "strings"

// renderBarsOutline draws only the top edge of each bar as a horizontal line,
// with empty space below — a minimal line-graph style visualizer.
func (v *Visualizer) renderBarsOutline(bands []float64) string {
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
			if level < rowTop && level > rowBottom {
				for range bw {
					sb.WriteRune('─')
				}
			} else {
				for range bw {
					sb.WriteByte(' ')
				}
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
