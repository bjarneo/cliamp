package ui

import "strings"

// renderBricks draws solid block columns with visible gaps between rows and bands.
// Uses half-height blocks (▄) so each brick is half a terminal row, with blank
// gaps between them, keeping total height equal to the bars visualizer.
func (v *Visualizer) renderBricks(bands []float64) string {
	height := v.Rows
	bandCount := len(bands)
	width := v.columns()

	var sb strings.Builder
	sb.Grow(height * (width*3 + 24))

	for row := range height {
		if row > 0 {
			sb.WriteByte('\n')
		}
		rowThreshold := float64(height-1-row) / float64(height)
		style := specANSI[specTag(rowThreshold)]
		if style.prefix != "" {
			sb.WriteString(style.prefix)
		}

		for i, level := range bands {
			bw := visBandWidth(bandCount, i, width)
			if level > rowThreshold {
				for range bw {
					sb.WriteString("▄")
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
