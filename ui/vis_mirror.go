package ui

import "math"

const mirrorSpanPercent = 84

// renderMirror draws one vertical bar per spectrum slot around a persistent
// horizontal axis. Braille subcells preserve the taper and narrow gaps in a
// small terminal panel.
func (v *Visualizer) renderMirror(bands []float64) string {
	dotRows := v.Rows * 4
	dotCols := PanelWidth * 2
	span := max(2, dotCols*mirrorSpanPercent/100)
	span = min(dotCols, span-span%2)
	barCount := max(1, span/2)
	x0 := (dotCols - span) / 2
	axisY := dotRows / 2
	maxRadius := min(axisY, dotRows-1-axisY)

	v.mirrorGrid.ensure(dotRows, dotCols)
	for x := x0; x < x0+span; x++ {
		v.mirrorGrid.set(x, axisY, 1)
	}

	env := 0.0
	for _, level := range bands {
		env += max(0, min(1, level))
	}
	if len(bands) > 0 {
		env /= float64(len(bands))
	}

	t := float64(v.Frame()) * TickAnim.Seconds()
	halfBars := float64(barCount-1) / 2
	for i := range barCount {
		distance := 0.0
		if halfBars > 0 {
			distance = math.Abs(float64(i)-halfBars) / halfBars
		}
		wobble := 0.4 + 0.6*math.Abs(math.Sin(t*4.6+float64(i)*0.42)*math.Sin(t*1.9-float64(i)*0.13))
		amplitude := float64(dotRows) * 0.80 * (1 - distance*0.55) * (0.3 + 0.7*env) * (0.35 + 0.65*wobble)
		radius := min(maxRadius, max(1, int(math.Round(amplitude))))
		x := x0 + i*2 + 1

		for y := axisY - radius; y <= axisY+radius; y++ {
			tier := int8(2)
			distanceToAxis := y - axisY
			if distanceToAxis < 0 {
				distanceToAxis = -distanceToAxis
			}
			if float64(distanceToAxis)/float64(radius) >= 0.75 {
				tier = 3
			}
			v.mirrorGrid.set(x, y, tier)
		}
	}

	return v.mirrorGrid.render(v.Rows)
}
