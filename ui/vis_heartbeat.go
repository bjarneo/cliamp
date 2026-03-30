package ui

import (
	"math"
	"strings"
)

// renderHeartbeat draws a scrolling ECG/pulse-monitor trace using Braille dots.
// Bass energy triggers sharp QRS-complex spikes; silence produces a flat line.
// The trace scrolls left each frame for the classic hospital-monitor look.
func (v *Visualizer) renderHeartbeat() string {
	height := v.Rows
	dotRows := height * 4
	dotCols := PanelWidth * 2

	samples := v.waveBuf
	n := len(samples)

	// Build a y-position for each dot column from raw audio.
	ypos := make([]int, dotCols)
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

	grid := make([]bool, dotRows*dotCols)

	// Draw the ECG trace with continuous line connections.
	for x := range dotCols {
		y := ypos[x]
		grid[y*dotCols+x] = true
		if x > 0 {
			lo, hi := min(y, ypos[x-1]), max(y, ypos[x-1])
			for fy := lo; fy <= hi; fy++ {
				grid[fy*dotCols+x] = true
			}
		}
	}

	// Draw a faint baseline at center.
	baseY := dotRows / 2
	for x := range dotCols {
		if !grid[baseY*dotCols+x] {
			// Dashed baseline: on for 6, off for 4.
			if (x/6)%2 == 0 {
				grid[baseY*dotCols+x] = true
			}
		}
	}

	// Render braille characters.
	lines := make([]string, height)
	for row := range height {
		var sb, run strings.Builder
		tag := -1

		for ch := range PanelWidth {
			var braille rune = '\u2800'
			hasTrace := false

			for dr := range 4 {
				for dc := range 2 {
					dy := row*4 + dr
					dx := ch*2 + dc
					if grid[dy*dotCols+dx] {
						braille |= brailleBit[dr][dc]
						// Check if this is part of the trace (not baseline).
						if dy != baseY {
							hasTrace = true
						}
					}
				}
			}

			newTag := 0 // green baseline
			if hasTrace {
				newTag = 2 // red trace
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
