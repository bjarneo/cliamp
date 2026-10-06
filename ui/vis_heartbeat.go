package ui

import "math"

// renderHeartbeat draws a scrolling ECG/pulse-monitor trace using Braille dots.
// Bass energy triggers sharp QRS-complex spikes; silence produces a flat line.
// The trace scrolls left each frame for the classic hospital-monitor look.
func (v *Visualizer) renderHeartbeat() string {
	height := v.Rows
	dotRows := height * 4
	dotCols := v.columns() * 2

	samples := v.waveBuf
	n := len(samples)

	// Build a y-position for each dot column from raw audio.
	ypos := v.traceYs(dotCols)
	centerY := float64(dotRows) / 2.0
	amplitude := float64(dotRows) * 0.45

	for x := range dotCols {
		var sample float64
		if n > 0 {
			idx := x * n / dotCols
			if idx >= n {
				idx = n - 1
			}
			sample = samples[idx]
		}

		// Shape the waveform like an ECG trace: sharpen peaks, flatten noise.
		shaped := sample * math.Abs(sample) // square the magnitude, keep sign
		y := int(centerY - shaped*amplitude)
		ypos[x] = max(0, min(dotRows-1, y))
	}

	// The trace is red (high tier) and the baseline green (low tier). A trace
	// dot on the baseline row counts as baseline, so a cell turns red only
	// where the trace leaves the baseline.
	baseY := dotRows / 2
	traceTier := func(y int) int8 {
		if y == baseY {
			return 1
		}
		return 3
	}
	grid := &v.dotGrid
	grid.ensure(dotRows, dotCols)

	// Draw the ECG trace with continuous line connections.
	for x := range dotCols {
		y := ypos[x]
		grid.set(x, y, traceTier(y))
		if x > 0 {
			lo, hi := min(y, ypos[x-1]), max(y, ypos[x-1])
			for fy := lo; fy <= hi; fy++ {
				grid.set(x, fy, traceTier(fy))
			}
		}
	}

	// Draw a faint baseline at center. Dashed baseline: on for 6, off for 4.
	for x := range dotCols {
		if (x/6)%2 == 0 {
			grid.set(x, baseY, 1)
		}
	}

	return grid.render(height, v.columns())
}
