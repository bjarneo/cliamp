package ui

import (
	"math"
	"strings"
)

// renderRetro draws a retro 80s synthwave scene: a striped setting sun above
// the horizon, a smooth audio-reactive wave, and a perspective grid floor that
// scrolls toward the viewer. Uses Braille characters for sub-cell resolution.
func (v *Visualizer) renderRetro(bands []float64) string {
	height := v.Rows
	charCols := PanelWidth
	dotRows := height * 4
	dotCols := charCols * 2
	bandCount := len(bands)

	// Horizon at 40% from top — gives room for wave and sun above.
	horizonDot := max(dotRows*2/5, 2)
	floorRows := dotRows - horizonDot
	centerX := float64(dotCols-1) / 2.0

	// Flat dot grid (single allocation): 0=empty, 1=grid, 2=wave, 3=sun.
	grid := make([]byte, dotRows*dotCols)

	// ── SUN ── striped semicircle above horizon.
	sunR := float64(horizonDot) * 0.85
	for dy := range horizonDot {
		rowDist := float64(horizonDot - dy) // dots above horizon
		if rowDist > sunR {
			continue
		}
		halfW := math.Sqrt(sunR*sunR - rowDist*rowDist)

		// Bottom half of sun has horizontal stripe gaps.
		if rowDist < sunR*0.5 {
			sw := max(1, int(sunR*0.15))
			if (int(rowDist)/sw)%2 == 1 {
				continue
			}
		}

		left := max(0, int(centerX-halfW))
		right := min(dotCols-1, int(centerX+halfW))
		off := dy * dotCols
		for dx := left; dx <= right; dx++ {
			grid[off+dx] = 3
		}
	}

	// ── HORIZON LINE ──
	off := horizonDot * dotCols
	for dx := range dotCols {
		grid[off+dx] = 1
	}

	// ── PERSPECTIVE GRID FLOOR ──

	// Vertical lines converging to vanishing point at (centerX, horizonDot).
	const numVLines = 18
	for i := range numVLines + 1 {
		bottomX := float64(i) * float64(dotCols-1) / float64(numVLines)
		for dy := horizonDot + 1; dy < dotRows; dy++ {
			t := float64(dy-horizonDot) / float64(max(1, floorRows-1))
			screenX := centerX + (bottomX-centerX)*t
			ix := int(math.Round(screenX))
			if ix >= 0 && ix < dotCols {
				grid[dy*dotCols+ix] = 1
			}
		}
	}

	// Horizontal lines scrolling toward the viewer.
	scroll := math.Mod(float64(v.frame)*0.08, 1.0)
	const numHLines = 10
	for i := range numHLines {
		z := (float64(i) + scroll) / float64(numHLines)
		if z > 1.0 {
			z -= 1.0
		}
		// Quadratic perspective: dense near horizon, spread near viewer.
		dy := horizonDot + 1 + int(z*z*float64(max(1, floorRows-2)))
		if dy > horizonDot && dy < dotRows {
			off := dy * dotCols
			for dx := range dotCols {
				grid[off+dx] = 1
			}
		}
	}

	// ── AUDIO WAVE AT HORIZON ──
	waveY := make([]int, dotCols)
	maxWave := float64(horizonDot) * 0.85
	for dx := range dotCols {
		bandF := float64(dx) / float64(max(1, dotCols-1)) * float64(bandCount-1)
		bi := int(bandF)
		frac := bandF - float64(bi)

		// Cosine interpolation for a smooth curve between bands.
		t := (1 - math.Cos(frac*math.Pi)) / 2

		var level float64
		if bi >= bandCount-1 {
			level = bands[bandCount-1]
		} else {
			level = bands[bi]*(1-t) + bands[bi+1]*t
		}

		// Small floor so the wave never fully vanishes.
		level = max(0.03, level)

		wy := horizonDot - int(level*maxWave)
		waveY[dx] = max(0, min(dotRows-1, wy))
	}

	// Draw wave with continuous line connections.
	for dx := range dotCols {
		y := waveY[dx]
		grid[y*dotCols+dx] = 2
		if dx > 0 {
			lo, hi := min(y, waveY[dx-1]), max(y, waveY[dx-1])
			for fy := lo; fy <= hi; fy++ {
				grid[fy*dotCols+dx] = 2
			}
		}
	}

	// ── RENDER BRAILLE ──
	lines := make([]string, height)
	for row := range height {
		var sb, run strings.Builder
		tag := -1
		base := row * 4

		for ch := range charCols {
			var braille rune = '\u2800'
			colBase := ch * 2
			hasWave, hasSun := false, false

			for dr := range 4 {
				for dc := range 2 {
					dy := base + dr
					dx := colBase + dc
					if dy >= dotRows || dx >= dotCols {
						continue
					}
					switch grid[dy*dotCols+dx] {
					case 1:
						braille |= brailleBit[dr][dc]
					case 2:
						braille |= brailleBit[dr][dc]
						hasWave = true
					case 3:
						braille |= brailleBit[dr][dc]
						hasSun = true
					}
				}
			}

			// Priority: wave (red) > sun (yellow) > grid (green).
			var newTag int
			switch {
			case hasWave:
				newTag = 2
			case hasSun:
				newTag = 1
			default:
				newTag = 0
			}
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
