package ui

import (
	"math"
	"strings"
)

// renderLightning draws jagged lightning bolts using Braille dots. Treble
// energy triggers bolt strikes from the top; each bolt forks and fades over
// a short lifecycle. Multiple bolts can be active simultaneously.
func (v *Visualizer) renderLightning(bands []float64) string {
	height := v.Rows
	dotRows := height * 4
	dotCols := panelWidth * 2
	bandCount := len(bands)

	grid := make([]bool, dotRows*dotCols)

	// Treble energy drives bolt intensity.
	trebleStart := bandCount * 3 / 5
	var trebleEnergy float64
	for b := trebleStart; b < bandCount; b++ {
		trebleEnergy += bands[b]
	}
	trebleEnergy /= float64(max(1, bandCount-trebleStart))

	// Total energy for number of bolts.
	var totalEnergy float64
	for _, e := range bands {
		totalEnergy += e
	}
	avgEnergy := totalEnergy / float64(bandCount)

	numBolts := 2 + int(avgEnergy*8)
	cycleLen := uint64(20)

	for i := range numBolts {
		cycle := (v.frame + uint64(i)*5) / cycleLen
		seed := cycle*104729 + uint64(i)*7919

		// Stagger bolt starts.
		offset := uint64(i) * cycleLen / uint64(numBolts)
		localFrame := (v.frame + offset) % cycleLen

		// Fade out over lifecycle.
		fade := 1.0 - float64(localFrame)/float64(cycleLen)
		if fade < 0.2 {
			continue // bolt has faded
		}

		// Start position: spread across the top.
		startX := int(seed%uint64(dotCols-4)) + 2

		// Draw the bolt: zig-zag downward.
		x := startX
		maxDepth := int(float64(dotRows) * (0.3 + trebleEnergy*0.7))
		if int(localFrame) > maxDepth/2 {
			maxDepth = min(maxDepth, dotRows)
		}

		for dy := 0; dy < maxDepth; dy++ {
			// Jitter: bolt zigs left or right.
			jitterSeed := seed + uint64(dy)*3037
			jitter := int(jitterSeed%5) - 2 // -2 to +2
			x += jitter
			x = max(1, min(dotCols-2, x))

			// Bolt thickness: wider near top, thinner at tip.
			thickness := max(1, 3-dy*3/dotRows)
			for t := -thickness; t <= thickness; t++ {
				px := x + t
				if px >= 0 && px < dotCols && dy < dotRows {
					// Stochastic fade for thinner appearance.
					if math.Abs(float64(t)) > 0 && scatterHash(i, dy, t+3, v.frame) > fade*0.5 {
						continue
					}
					grid[dy*dotCols+px] = true
				}
			}

			// Fork: small branch splits off occasionally.
			if dy > 4 && dy%6 == 0 && trebleEnergy > 0.3 {
				forkDir := 1
				if jitterSeed%2 == 0 {
					forkDir = -1
				}
				fx := x
				for fd := range min(8, dotRows-dy) {
					fx += forkDir + int(jitterSeed+uint64(fd))%2 - 1
					fy := dy + fd
					fx = max(0, min(dotCols-1, fx))
					if fy < dotRows {
						grid[fy*dotCols+fx] = true
					}
				}
			}
		}
	}

	// Render braille with row-based coloring: top bright, bottom dim.
	lines := make([]string, height)
	for row := range height {
		var content strings.Builder
		for ch := range panelWidth {
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
		lines[row] = specStyle(float64(height-1-row) / float64(height)).Render(content.String())
	}

	return strings.Join(lines, "\n")
}
