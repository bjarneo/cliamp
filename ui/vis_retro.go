package ui

import "math"

// renderRetro draws a retro 80s synthwave scene: a striped setting sun above
// the horizon, a smooth audio-reactive wave, and a perspective grid floor that
// scrolls toward the viewer. Uses Braille characters for sub-cell resolution.
func (v *Visualizer) renderRetro(bands []float64) string {
	height := v.Rows
	charCols := v.columns()
	dotRows := height * 4
	dotCols := charCols * 2
	bandCount := len(bands)

	// Horizon at 40% from top gives room for the wave and sun above.
	horizonDot := max(dotRows*2/5, 2)
	floorRows := dotRows - horizonDot
	centerX := float64(dotCols-1) / 2.0

	// Tier priority: grid is 1, sun is 2, and wave is 3.
	const (
		retroGridTier int8 = 1
		retroSunTier  int8 = 2
		retroWaveTier int8 = 3
	)
	grid := &v.dotGrid
	grid.ensure(dotRows, dotCols)

	// Striped semicircle sun above the horizon.
	sunR := float64(horizonDot) * 0.85
	for dy := range horizonDot {
		rowDist := float64(horizonDot - dy)
		if rowDist > sunR {
			continue
		}
		halfW := math.Sqrt(sunR*sunR - rowDist*rowDist)

		if rowDist < sunR*0.5 {
			sw := max(1, int(sunR*0.15))
			if (int(rowDist)/sw)%2 == 1 {
				continue
			}
		}

		left := max(0, int(centerX-halfW))
		right := min(dotCols-1, int(centerX+halfW))
		for dx := left; dx <= right; dx++ {
			grid.set(dx, dy, retroSunTier)
		}
	}

	// Horizon line.
	for dx := range dotCols {
		grid.set(dx, horizonDot, retroGridTier)
	}

	// Vertical floor lines converging to the vanishing point.
	const numVLines = 18
	for i := range numVLines + 1 {
		bottomX := float64(i) * float64(dotCols-1) / float64(numVLines)
		for dy := horizonDot + 1; dy < dotRows; dy++ {
			t := float64(dy-horizonDot) / float64(max(1, floorRows-1))
			screenX := centerX + (bottomX-centerX)*t
			ix := int(math.Round(screenX))
			grid.set(ix, dy, retroGridTier)
		}
	}

	// Horizontal floor lines scrolling toward the viewer.
	scroll := math.Mod(float64(v.frame)*0.08, 1.0)
	const numHLines = 10
	for i := range numHLines {
		z := (float64(i) + scroll) / float64(numHLines)
		if z > 1.0 {
			z -= 1.0
		}
		dy := horizonDot + 1 + int(z*z*float64(max(1, floorRows-2)))
		if dy > horizonDot && dy < dotRows {
			for dx := range dotCols {
				grid.set(dx, dy, retroGridTier)
			}
		}
	}

	// Audio wave at the horizon.
	waveY := v.traceYs(dotCols)
	maxWave := float64(horizonDot) * 0.85
	for dx := range dotCols {
		var level float64
		if bandCount > 0 {
			bandF := float64(dx) / float64(max(1, dotCols-1)) * float64(bandCount-1)
			bi := int(bandF)
			frac := bandF - float64(bi)
			t := (1 - math.Cos(frac*math.Pi)) / 2
			if bi >= bandCount-1 {
				level = bands[bandCount-1]
			} else {
				level = bands[bi]*(1-t) + bands[bi+1]*t
			}
		}
		level = max(0.03, level)

		wy := horizonDot - int(level*maxWave)
		waveY[dx] = max(0, min(dotRows-1, wy))
	}

	// Connect consecutive wave points vertically.
	for dx := range dotCols {
		y := waveY[dx]
		grid.set(dx, y, retroWaveTier)
		if dx > 0 {
			lo, hi := min(y, waveY[dx-1]), max(y, waveY[dx-1])
			for fy := lo; fy <= hi; fy++ {
				grid.set(dx, fy, retroWaveTier)
			}
		}
	}

	return grid.render(height, charCols)
}
