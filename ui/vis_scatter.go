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
	width := v.columns()

	for row := range height {
		var content strings.Builder

		for b := range bandCount {
			charsPerBand := visBandWidth(bandCount, b, width)
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
			if bandGapAfter(bandCount, b, width) {
				content.WriteByte(' ')
			}
		}
		lines[row] = specWrap(float64(height-1-row)/float64(height), content.String())
	}

	return strings.Join(lines, "\n")
}

// scatterHash returns a pseudo-random value in [0, 1) for a given dot position
// and frame. Dots persist for a few frames to create a twinkling effect.
func scatterHash(band, row, col int, frame uint64) float64 {
	// Stagger per-dot so they don't all change simultaneously.
	f := (frame + uint64(row*3+col)) / 3
	h := uint64(band)*7919 + uint64(row)*6271 + uint64(col)*3037 + f*104729
	h ^= h >> 16
	h *= 0x45d9f3b37197344b
	h ^= h >> 16
	return float64(h%10000) / 10000.0
}
