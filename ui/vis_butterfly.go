package ui

import (
	"math"
	"strings"
)

// renderButterfly draws a symmetric Rorschach/butterfly pattern using Braille
// dots. The spectrum bands are mirrored horizontally from the center, and
// organic variation is added via sine wobble and scatterHash to create
// ink-blot-like shapes that pulse with the music.
func (v *Visualizer) renderButterfly(bands []float64) string {
	height := v.Rows
	dotRows := height * 4
	dotCols := PanelWidth * 2
	centerX := dotCols / 2
	bandCount := len(bands)

	grid := make([]bool, dotRows*dotCols)

	for dy := range dotRows {
		// Map vertical position to a band index.
		bandF := float64(dy) / float64(max(1, dotRows-1)) * float64(bandCount-1)
		bi := int(bandF)
		frac := bandF - float64(bi)
		var energy float64
		if bi >= bandCount-1 {
			energy = bands[bandCount-1]
		} else {
			energy = bands[bi]*(1-frac) + bands[bi+1]*frac
		}

		// Wing width: how far from center the pattern extends.
		t := float64(v.frame)*0.08 + float64(dy)*0.3
		wobble := math.Sin(t) * 0.15
		wingWidth := int(float64(centerX) * (energy + wobble) * 0.9)

		for dx := range wingWidth {
			// Distance from center normalized to wing width.
			norm := float64(dx) / float64(max(1, wingWidth))

			// Organic edge: denser near center, sparser at edges.
			threshold := (1.0 - norm*norm) * energy
			// Add frame-based flicker at the edges.
			if norm > 0.6 {
				threshold *= 0.5 + 0.5*math.Sin(float64(v.frame)*0.1+float64(dy)*0.5+float64(dx)*0.3)
			}

			if scatterHash(bi, dy, dx, v.frame/3) < threshold {
				// Right wing.
				rx := centerX + dx
				if rx < dotCols {
					grid[dy*dotCols+rx] = true
				}
				// Left wing (mirror).
				lx := centerX - 1 - dx
				if lx >= 0 {
					grid[dy*dotCols+lx] = true
				}
			}
		}

		// Central spine — always drawn.
		if energy > 0.05 {
			grid[dy*dotCols+centerX] = true
			if centerX > 0 {
				grid[dy*dotCols+centerX-1] = true
			}
		}
	}

	// Render braille with row-based coloring.
	lines := make([]string, height)
	for row := range height {
		var content strings.Builder
		for ch := range PanelWidth {
			var braille rune = '\u2800'
			for dr := range 4 {
				for dc := range 2 {
					if grid[(row*4+dr)*dotCols+ch*2+dc] {
						braille |= brailleBit[dr][dc]
					}
				}
			}
			content.WriteRune(braille)
		}
		// Color gradient from top to bottom.
		norm := float64(row) / float64(max(1, height-1))
		lines[row] = specStyle(norm).Render(content.String())
	}

	return strings.Join(lines, "\n")
}
