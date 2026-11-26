package ui

import "strings"

// renderBarsDot draws bars using Braille dot patterns instead of solid blocks.
// Each terminal cell maps to a 4x2 Braille grid, so dots are filled from the
// bottom up proportionally to the band level, giving a stippled texture.
func (v *Visualizer) renderBarsDot(bands []float64) string {
	height := v.Rows
	dotRows := height * 4
	bandCount := len(bands)
	width := v.columns()

	var sb strings.Builder
	sb.Grow(height * (width*3 + 24))
	w := styleRunWriter{sb: &sb}

	for row := range height {
		if row > 0 {
			sb.WriteByte('\n')
		}
		curTag := -1
		w.setTag(-1)
		rowTag := specTag(float64(height-1-row) / float64(height))

		for b := range bandCount {
			charsPerBand := visBandWidth(bandCount, b, width)
			if charsPerBand > 0 {
				var mask byte
				level := bands[b]
				for dr := range 4 {
					dotRow := row*4 + dr
					// Invert so bars grow from the bottom.
					dotY := float64(dotRows-1-dotRow) / float64(dotRows)
					if dotY < level {
						mask |= brailleBit[dr][0] | brailleBit[dr][1]
					}
				}
				if rowTag != curTag {
					curTag = rowTag
					w.setTag(curTag)
				}
				for range charsPerBand {
					w.writeBraille(mask)
				}
			}

			if bandGapAfter(bandCount, b, width) {
				// Gap character inherits the active style run.
				w.writeByte(' ')
			}
		}
		w.flush()
	}

	return sb.String()
}
