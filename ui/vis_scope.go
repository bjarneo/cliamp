package ui

import (
	"math"
	"strings"
)

// renderScope draws a Lissajous-style XY oscilloscope using Braille dots.
// Since the audio tap is mono, a phase-delayed copy of the signal is used as
// the Y axis. The delay slowly oscillates over time, producing continuously
// evolving Lissajous figures — circles for pure tones, complex knots for music.
func (v *Visualizer) renderScope() string {
	height := v.Rows
	dotRows := height * 4
	dotCols := PanelWidth * 2

	samples := v.waveBuf
	n := len(samples)

	grid := make([]bool, dotRows*dotCols)

	if n > 1 {
		// Phase delay slowly oscillates for evolving Lissajous patterns.
		baseDelay := n / 4
		wobble := int(math.Sin(float64(v.frame)*0.02) * float64(n/8))
		delay := max(1, min(n-1, baseDelay+wobble))

		// Plot ~512 XY pairs for a dense, smooth figure.
		plotPoints := min(n-delay, 512)
		step := max(1, (n-delay)/plotPoints)

		var prevDotX, prevDotY int
		first := true

		for i := 0; i+delay < n; i += step {
			x := samples[i]
			y := samples[i+delay]

			// Map [-1, 1] to dot coordinates.
			dotX := int((x + 1.0) * 0.5 * float64(dotCols-1))
			dotY := int((1.0 - y) * 0.5 * float64(dotRows-1))
			dotX = max(0, min(dotCols-1, dotX))
			dotY = max(0, min(dotRows-1, dotY))

			grid[dotY*dotCols+dotX] = true

			// Interpolate between consecutive points for smoother curves.
			if !first {
				dx := dotX - prevDotX
				dy := dotY - prevDotY
				adx := dx
				if adx < 0 {
					adx = -adx
				}
				ady := dy
				if ady < 0 {
					ady = -ady
				}
				steps := max(adx, ady)
				if steps > 0 && steps < 30 {
					for s := 1; s < steps; s++ {
						mx := prevDotX + dx*s/steps
						my := prevDotY + dy*s/steps
						if mx >= 0 && mx < dotCols && my >= 0 && my < dotRows {
							grid[my*dotCols+mx] = true
						}
					}
				}
			}

			prevDotX = dotX
			prevDotY = dotY
			first = false
		}
	}

	// Convert dot grid to Braille characters.
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
		lines[row] = specStyle(float64(height-1-row) / float64(height)).Render(content.String())
	}

	return strings.Join(lines, "\n")
}
