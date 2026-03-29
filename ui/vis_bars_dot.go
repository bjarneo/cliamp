package ui

import "strings"

// renderBarsDot draws bars using Braille dot patterns instead of solid blocks.
// Each terminal cell maps to a 4x2 Braille grid, so dots are filled from the
// bottom up proportionally to the band level, giving a stippled texture.
func (v *Visualizer) renderBarsDot(bands []float64) string {
	height := v.Rows
	dotRows := height * 4
	lines := make([]string, height)
	bandCount := len(bands)

	for row := range height {
		var run strings.Builder
		var sb strings.Builder
		curTag := -1

		for b := range bandCount {
			charsPerBand := visBandWidth(bandCount, b)
			for c := range charsPerBand {
				var braille rune = '\u2800'

				for dr := range 4 {
					for dc := range 2 {
						dotRow := row*4 + dr
						// Invert: bars grow from bottom.
						dotY := float64(dotRows-1-dotRow) / float64(dotRows)
						if dotY < bands[b] {
							braille |= brailleBit[dr][dc]
						}
					}
				}

				norm := float64(height-1-row) / float64(height)
				tag := specTag(norm)
				if tag != curTag {
					flushStyleRun(&sb, &run, curTag)
					curTag = tag
				}
				run.WriteRune(braille)

				// Add gap between characters within the same band.
				_ = c
			}

			if b < bandCount-1 {
				// Gap character inherits current style run.
				run.WriteByte(' ')
			}
		}
		flushStyleRun(&sb, &run, curTag)
		lines[row] = sb.String()
	}

	return strings.Join(lines, "\n")
}
