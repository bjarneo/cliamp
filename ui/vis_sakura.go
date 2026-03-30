package ui

import (
	"math"
	"strings"
)

// sakuraShapes defines petal silhouettes as (row, col) dot offsets from center.
// Large shapes represent close petals, small ones distant petals.
var sakuraShapes = [][][2]int{
	// Large — 6 dots, wide teardrop
	{{0, 1}, {1, 0}, {1, 1}, {1, 2}, {2, 0}, {2, 1}},
	{{0, 1}, {1, 0}, {1, 1}, {1, 2}, {2, 1}, {2, 2}},
	{{0, 1}, {0, 2}, {1, 0}, {1, 1}, {1, 2}, {2, 1}},
	// Medium — 4 dots
	{{0, 1}, {1, 0}, {1, 1}, {2, 0}},
	{{0, 0}, {1, 0}, {1, 1}, {2, 1}},
	{{0, 0}, {0, 1}, {1, 1}, {2, 1}},
	// Small — 2-3 dots, distant
	{{0, 0}, {1, 1}},
	{{0, 1}, {1, 0}},
	{{0, 0}, {0, 1}, {1, 0}},
}

// renderSakura draws individual cherry blossom petals drifting slowly downward.
// Each petal is a small Braille silhouette with its own fall speed and gentle
// lateral sway. Energy controls how many petals are on screen — quiet passages
// show a sparse, contemplative drift while louder music fills the air.
func (v *Visualizer) renderSakura(bands []float64) string {
	height := v.Rows
	dotRows := height * 4
	dotCols := PanelWidth * 2

	grid := make([]bool, dotRows*dotCols)

	var totalEnergy float64
	for _, e := range bands {
		totalEnergy += e
	}
	avgEnergy := totalEnergy / float64(len(bands))

	// 12 petals at silence, up to 28 when loud.
	numPetals := 12 + int(avgEnergy*16)

	for p := range numPetals {
		seed := uint64(p)*104729 + 7919

		// Shape — first 3 are large, next 3 medium, last 3 small.
		shapeIdx := int(seed * 4391 % uint64(len(sakuraShapes)))
		shape := sakuraShapes[shapeIdx]

		// Large shapes fall slower (close), small ones faster (distant).
		fallSpeed := 1
		if shapeIdx >= 6 {
			fallSpeed = 2
		}

		// X: spread across entire panel width.
		baseX := int(seed % uint64(dotCols))

		// Y: slow scroll with off-screen buffer for smooth entry/exit.
		wrapH := dotRows + 10
		baseY := int((seed * 3037) % uint64(wrapH))
		y := (baseY+int(v.frame)*fallSpeed/8)%wrapH - 5

		// Gentle lateral sway — each petal has its own phase.
		swayPhase := float64(seed%1000) / 1000.0 * 2 * math.Pi
		sway := math.Sin(float64(v.frame)*0.015+swayPhase) * 3.0
		x := baseX + int(sway)

		// Stamp petal onto grid.
		for _, dot := range shape {
			dr := y + dot[0]
			dc := x + dot[1]
			if dr >= 0 && dr < dotRows && dc >= 0 && dc < dotCols {
				grid[dr*dotCols+dc] = true
			}
		}
	}

	// Convert dot grid to Braille characters.
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
		// Top rows bright, bottom dimmer.
		lines[row] = specStyle(float64(height-1-row) / float64(height)).Render(content.String())
	}

	return strings.Join(lines, "\n")
}
