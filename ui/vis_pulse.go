package ui

import (
	"math"
	"strings"
)

// pulseCoords holds per-dot distance-from-center and angle values for the
// current panel dimensions. Recomputed lazily on resize so the hot render
// loop can skip ~3360 sqrt and atan2 calls per frame (5 rows × 84 cols × 8
// dots) and read from flat arrays instead.
type pulseCoords struct {
	width, height int
	maxR          float64
	dist          []float64
	angle         []float64
}

func (v *Visualizer) pulseCoords() *pulseCoords {
	height := v.Rows
	width := v.columns()
	if c := v.pulseCoordCache; c != nil && c.width == width && c.height == height {
		return c
	}
	dotRows := height * 4
	dotCols := width * 2
	centerX := float64(dotCols) / 2.0
	centerY := float64(dotRows) / 2.0
	xScale := centerY / centerX

	size := height * width * 8
	c := &pulseCoords{
		width:  width,
		height: height,
		maxR:   centerY - 1,
		dist:   make([]float64, size),
		angle:  make([]float64, size),
	}
	for row := range height {
		for col := range width {
			for dr := range 4 {
				for dc := range 2 {
					dx := (float64(col*2+dc) - centerX) * xScale
					dy := float64(row*4+dr) - centerY
					idx := pulseDotIndex(row, col, dr, dc, width)
					c.dist[idx] = math.Sqrt(dx*dx + dy*dy)
					a := math.Atan2(dy, dx)
					if a < 0 {
						a += 2 * math.Pi
					}
					c.angle[idx] = a
				}
			}
		}
	}
	v.pulseCoordCache = c
	return c
}

func pulseDotIndex(row, col, dr, dc, width int) int {
	return ((row*width+col)*4+dr)*2 + dc
}

// renderPulse draws a pulsating ellipse using Braille dots that fills the
// display width. The radius at each angle blends per-band frequency energy
// with the overall level so the whole shape surges on every beat while still
// deforming per frequency. A clean shockwave ring radiates outward on
// transients. The interior is solid-filled with an anti-aliased edge and a
// green→yellow→red radial color gradient.
func (v *Visualizer) renderPulse(bands []float64) string {
	coords := v.pulseCoords()
	height := v.Rows
	width := v.columns()
	bandCount := len(bands)
	maxR := coords.maxR

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

	// Per-frame rotation offset — added uniformly to every cached angle.
	rotOffset := float64(v.frame) * (0.015 + avgEnergy*0.04)
	twoPi := 2 * math.Pi
	bandScale := float64(bandCount) / twoPi

	lines := make([]string, height)

	for row := range height {
		var sb, run strings.Builder
		tag := -1

		for col := range width {
			var braille rune = '\u2800'
			var maxNorm float64

			for dr := range 4 {
				for dc := range 2 {
					idx := pulseDotIndex(row, col, dr, dc, width)
					dist := coords.dist[idx]

					rotAngle := coords.angle[idx] + rotOffset
					rotAngle -= math.Floor(rotAngle/twoPi) * twoPi

					// Cosine-interpolated band mapping.
					bandPos := rotAngle * bandScale
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
						if scatterHash(bandIdx, row*4+dr, col*2+dc, v.frame) < edgeFade*0.7 {
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
