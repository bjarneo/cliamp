package ui

import "strings"

// renderScatter draws a twinkling particle field using Braille dots.
// Dot density per band is proportional to the squared energy level, with a
// gravity bias that makes particles denser near the bottom.
func (v *Visualizer) renderScatter(bands []float64) string {
	height := v.Rows
	dotRows := height * 4
	lines := make([]string, height)
	bandCount := len(bands)

	for row := range height {
		var content strings.Builder

		for b := range bandCount {
			charsPerBand := visBandWidth(bandCount, b)
			for c := range charsPerBand {
				var braille rune = '\u2800'

				for dr := range 4 {
					for dc := range 2 {
						dotRow := row*4 + dr
						dotCol := c*2 + dc

						h := scatterHash(b, dotRow, dotCol, v.frame)

						// Gravity bias: more particles settle near the bottom.
						heightFactor := 0.5 + 0.5*float64(dotRow)/float64(dotRows-1)
						threshold := bands[b] * bands[b] * heightFactor

						if h < threshold {
							braille |= brailleBit[dr][dc]
						}
					}
				}

				content.WriteRune(braille)
			}
			if b < bandCount-1 {
				content.WriteByte(' ')
			}
		}
		lines[row] = specStyle(float64(height-1-row) / float64(height)).Render(content.String())
	}

	return strings.Join(lines, "\n")
}
