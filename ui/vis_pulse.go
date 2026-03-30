package ui

import (
	"math"
	"strings"
)

// renderPulse draws a pulsating ellipse using Braille dots that fills the
// display width. The radius at each angle blends per-band frequency energy
// with the overall level so the whole shape surges on every beat while still
// deforming per frequency. A clean shockwave ring radiates outward on
// transients. The interior is solid-filled with an anti-aliased edge and a
// green→yellow→red radial color gradient.
func (v *Visualizer) renderPulse(bands []float64) string {
	height := v.Rows
	dotRows := height * 4
	dotCols := PanelWidth * 2
	bandCount := len(bands)

	centerX := float64(dotCols) / 2.0
	centerY := float64(dotRows) / 2.0

	// Squash x so the shape stretches to fill the wide terminal panel.
	xScale := centerY / centerX
	maxR := centerY - 1

	var totalEnergy float64
	for _, e := range bands {
		totalEnergy += e
	}
	avgEnergy := totalEnergy / float64(bandCount)

	// Shockwave: expanding ring that fades as it grows.
	shockPhase := math.Mod(float64(v.frame)*0.10, 1.0)
	shockR := maxR * (0.3 + 0.7*shockPhase)
	shockStrength := avgEnergy * avgEnergy * (1.0 - shockPhase*shockPhase)

	// Gentle breathing keeps the shape alive during silence.
	breath := math.Sin(float64(v.frame)*0.05) * 0.02

	lines := make([]string, height)

	for row := range height {
		var sb, run strings.Builder
		tag := -1

		for c := range PanelWidth {
			var braille rune = '\u2800'
			var maxNorm float64

			for dr := range 4 {
				for dc := range 2 {
					dotX := float64(c*2 + dc)
					dotY := float64(row*4 + dr)

					dx := (dotX - centerX) * xScale
					dy := dotY - centerY
					dist := math.Sqrt(dx*dx + dy*dy)

					angle := math.Atan2(dy, dx)
					if angle < 0 {
						angle += 2 * math.Pi
					}

					// Subtle rotation, faster with energy.
					rotAngle := angle + float64(v.frame)*(0.015+avgEnergy*0.04)
					rotAngle -= math.Floor(rotAngle/(2*math.Pi)) * 2 * math.Pi

					// Cosine-interpolated band mapping.
					bandPos := rotAngle / (2 * math.Pi) * float64(bandCount)
					bandIdx := int(bandPos) % bandCount
					nextBand := (bandIdx + 1) % bandCount
					frac := bandPos - math.Floor(bandPos)
					t := (1 - math.Cos(frac*math.Pi)) / 2
					energy := bands[bandIdx]*(1-t) + bands[nextBand]*t

					// Blend per-band with overall so the whole shape beats.
					blended := energy*0.6 + avgEnergy*0.4
					punch := blended * blended
					r := maxR * (0.08 + breath + 0.92*punch)

					// --- Solid fill ---
					if r > 0.5 && dist <= r {
						norm := dist / r
						if norm > maxNorm {
							maxNorm = norm
						}
						braille |= brailleBit[dr][dc]
					} else if r > 0.5 && dist < r+1.5 {
						// Anti-aliased edge.
						edgeFade := 1.0 - (dist-r)/1.5
						if scatterHash(bandIdx, row*4+dr, c*2+dc, v.frame) < edgeFade*0.7 {
							braille |= brailleBit[dr][dc]
							if maxNorm < 0.9 {
								maxNorm = 0.9
							}
						}
					}

					// --- Shockwave ring ---
					if shockStrength > 0.05 {
						shockDist := math.Abs(dist - shockR)
						shockThick := 0.6 + shockStrength*1.5
						if shockDist < shockThick {
							fade := 1.0 - shockDist/shockThick
							if fade > 0.4 {
								braille |= brailleBit[dr][dc]
								if maxNorm < 0.65 {
									maxNorm = 0.65
								}
							}
						}
					}
				}
			}

			// Radial color gradient: green core → yellow → red edge.
			newTag := specTag(maxNorm)
			if newTag != tag {
				flushStyleRun(&sb, &run, tag)
				tag = newTag
			}
			run.WriteRune(braille)
		}

		flushStyleRun(&sb, &run, tag)
		lines[row] = sb.String()
	}

	return strings.Join(lines, "\n")
}
