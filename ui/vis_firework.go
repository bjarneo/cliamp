package ui

import (
	"math"
	"strings"
)

// renderFirework draws firework bursts using Braille dots. Each burst launches
// from the bottom with a rising trail, then explodes into a sphere of particles
// that drift downward with gravity and fade. Audio energy drives the number of
// simultaneous bursts and the size of each explosion.
func (v *Visualizer) renderFirework(bands []float64) string {
	height := v.Rows
	dotRows := height * 4
	dotCols := PanelWidth * 2

	grid := make([]bool, dotRows*dotCols)

	var totalEnergy float64
	for _, e := range bands {
		totalEnergy += e
	}
	avgEnergy := totalEnergy / float64(len(bands))

	// Number of simultaneous firework bursts: 5 quiet, up to 14 loud.
	numBursts := 5 + int(avgEnergy*9)
	cycleLen := uint64(48)
	launchLen := uint64(10)

	for i := range numBursts {
		// Seed changes each cycle so bursts appear in new positions.
		cycle := (v.frame + uint64(i)*7) / cycleLen
		seed := cycle*104729 + uint64(i)*7919

		// Stagger starts so bursts don't all fire simultaneously.
		offset := (uint64(i)*cycleLen/uint64(numBursts) + (seed/3)%5)
		localFrame := (v.frame + offset) % cycleLen

		// Burst center — spread across the panel, upper portion.
		cx := int((seed * 6271) % uint64(dotCols))
		cy := int((seed*4391)%uint64(dotRows/2)) + dotRows/8

		// Associated band for energy-driven sizing.
		bandIdx := int(seed % uint64(len(bands)))
		energy := bands[bandIdx]

		if localFrame < launchLen {
			// Rising trail from bottom to burst center.
			progress := float64(localFrame) / float64(launchLen)
			trailY := dotRows - 1 - int(float64(dotRows-1-cy)*progress)
			// Short trail of a few dots.
			for dy := range 4 {
				ty := trailY + dy
				if ty >= 0 && ty < dotRows && cx >= 0 && cx < dotCols {
					grid[ty*dotCols+cx] = true
				}
			}
		} else {
			// Burst expansion and fade.
			burstT := float64(localFrame-launchLen) / float64(cycleLen-launchLen)

			maxRadius := 3.0 + energy*8.0
			// Fast expansion, then slow drift.
			radius := maxRadius * math.Min(burstT*3.0, 1.0)
			// Gravity pulls particles down over time.
			gravity := burstT * burstT * 5.0
			// Particles fade out over time.
			fade := max(0.0, 1.0-burstT*1.3)

			numParticles := 18 + int(energy*18)
			for p := range numParticles {
				angle := float64(p) / float64(numParticles) * 2 * math.Pi
				pSeed := seed + uint64(p)*2909
				speed := 0.6 + float64(pSeed%400)/1000.0

				px := cx + int(math.Cos(angle)*radius*speed)
				py := cy + int(math.Sin(angle)*radius*speed+gravity)

				// Stochastic fade — more particles disappear as time passes.
				if scatterHash(bandIdx, p, int(seed%100), v.frame) > fade {
					continue
				}

				if px >= 0 && px < dotCols && py >= 0 && py < dotRows {
					grid[py*dotCols+px] = true
				}
			}
		}
	}

	// Convert dot grid to Braille characters with row-based spectrum color.
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
		// Top rows bright (red), bottom dimmer (green) — fireworks in a night sky.
		lines[row] = specStyle(float64(height-1-row) / float64(height)).Render(content.String())
	}

	return strings.Join(lines, "\n")
}
