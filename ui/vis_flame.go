package ui

import (
	"math"
	"strings"
)

// renderFlame draws rising flame tendrils using Braille dots. Each band produces
// a column of flickering fire that rises proportionally to energy, with lateral
// wobble driven by a sine-based displacement for an organic, dancing look.
func (v *Visualizer) renderFlame(bands []float64) string {
	height := v.Rows
	dotRows := height * 4
	lines := make([]string, height)
	bandCount := len(bands)

	for row := range height {
		var content strings.Builder

		for b := range bandCount {
			charsPerBand := visBandWidth(bandCount, b)
			bandDotCols := charsPerBand * 2
			for c := range charsPerBand {
				var braille rune = '\u2800'

				for dr := range 4 {
					for dc := range 2 {
						dotRow := row*4 + dr
						dotCol := c*2 + dc

						// Invert: flames rise from bottom, so row 0 = top of flame.
						flameY := float64(dotRows-1-dotRow) / float64(dotRows-1)

						// Flame reaches up to flameY proportional to band level.
						if flameY > bands[b] {
							continue
						}

						// Lateral wobble: sine wave displaced by height and time.
						t := float64(v.frame) * 0.3
						wobble := math.Sin(t+flameY*6.0+float64(b)*2.1) * 1.5
						centerCol := float64(bandDotCols) / 2.0

						// Flame narrows toward the tip.
						tipNarrow := 1.0 - flameY/max(bands[b], 0.01)
						flameWidth := (0.3 + 0.7*tipNarrow) * centerCol

						dist := math.Abs(float64(dotCol) - centerCol + 0.5 - wobble) // distance from flame center
						if dist < flameWidth {
							// Add flicker at the edges using hash.
							edge := dist / flameWidth
							if edge < 0.7 || scatterHash(b, dotRow, dotCol, v.frame) < 0.6 {
								braille |= brailleBit[dr][dc]
							}
						}
					}
				}

				content.WriteRune(braille)
			}
			if b < bandCount-1 {
				content.WriteByte(' ')
			}
		}
		// Color: bottom rows (base) are red/hot, upper rows (tips) are green/cool.
		lines[row] = specStyle(float64(row) / float64(height)).Render(content.String())
	}

	return strings.Join(lines, "\n")
}
