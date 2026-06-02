package ui

import (
	"math"
	"strings"
)

// renderBubbles draws rising air bubbles using Braille dots. Each bubble is a
// hollow ring with a tiny specular highlight that drifts upward and sways
// laterally. Audio energy modulates the sway and highlight intensity; the
// bubble count is fixed so bubbles never pop in or out of existence mid-air.
// Bubbles fade stochastically as they approach the surface so they appear to
// "pop."
func (v *Visualizer) renderBubbles(bands []float64) string {
	height := v.Rows
	dotRows := height * 4
	dotCols := PanelWidth * 2
	if dotRows < 4 || dotCols < 4 {
		return strings.Repeat("\n", max(0, height-1))
	}

	grid := make([]bool, dotRows*dotCols)

	var totalEnergy float64
	for _, e := range bands {
		totalEnergy += e
	}
	avgEnergy := totalEnergy / float64(len(bands))

	// Fixed count — changing this per frame makes bubbles spawn/vanish mid-air.
	const numBubbles = 18

	for i := range numBubbles {
		seed := uint64(i)*104729 + 7919

		// Stable per-bubble radius (1.5 to 4.0 dots). Must not depend on
		// per-frame audio, otherwise trajectory parameters derived from it
		// (speedDiv, wrapH, baseY) jitter every frame and the bubble flashes
		// around the screen instead of rising smoothly.
		radius := 1.5 + float64(seed%100)/100.0*2.5

		// Bigger bubbles rise slower (buoyancy feels floaty). Lower divisor =
		// faster rise: at 20 FPS a divisor of ~4 means one dot every ~200ms,
		// crossing the panel in roughly 4 seconds.
		speedDiv := 3 + int(radius)

		// Continuous upward scroll with off-screen buffer for smooth entry/exit.
		wrapH := dotRows + int(radius*2) + 8
		baseY := int((seed * 3037) % uint64(wrapH))
		y := wrapH - 1 - ((baseY + int(v.frame)/speedDiv) % wrapH) - int(radius) - 2

		// Horizontal position with gentle sinusoidal sway. Amplitude scales
		// with overall energy — quiet passages drift calmly, loud passages
		// wobble a bit more. This only shifts x, so it can't destabilize the
		// trajectory.
		baseX := int(seed % uint64(dotCols))
		swayPhase := float64(seed%1000) / 1000.0 * 2 * math.Pi
		swayAmp := 1.5 + avgEnergy*2.5
		sway := math.Sin(float64(v.frame)*0.03+swayPhase) * swayAmp
		x := baseX + int(sway)

		// Pop fade — the last few rows thin the ring stochastically.
		popZone := int(radius) + 3
		popFade := 1.0
		if y < popZone {
			popFade = math.Max(0, float64(y)/float64(popZone))
		}

		// Draw hollow ring.
		rInner := radius - 0.9
		bbox := int(radius) + 1
		for dy := -bbox; dy <= bbox; dy++ {
			for dx := -bbox; dx <= bbox; dx++ {
				dist := math.Sqrt(float64(dx*dx + dy*dy))
				if dist > radius || dist < rInner {
					continue
				}
				// Stable per-bubble pop pattern (no frame dependency) so the
				// ring doesn't strobe as it fades near the top.
				if popFade < 1.0 && scatterHash(i, dy, dx, 0) > popFade {
					continue
				}
				gy := y + dy
				gx := x + dx
				if gy >= 0 && gy < dotRows && gx >= 0 && gx < dotCols {
					grid[gy*dotCols+gx] = true
				}
			}
		}

		// Specular highlight — small bright cluster in the upper-left quadrant.
		if radius >= 2.0 && popFade > 0.5 {
			hx := x - int(radius*0.45)
			hy := y - int(radius*0.45)
			for _, d := range [][2]int{{0, 0}, {0, 1}, {1, 0}} {
				gy := hy + d[0]
				gx := hx + d[1]
				if gy >= 0 && gy < dotRows && gx >= 0 && gx < dotCols {
					grid[gy*dotCols+gx] = true
				}
			}
		}
	}

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
		// Top rows warm (light through surface), bottom rows cool (depth).
		lines[row] = specWrap(float64(height-1-row)/float64(height), content.String())
	}

	return strings.Join(lines, "\n")
}
