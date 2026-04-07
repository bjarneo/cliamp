package ui

import "strings"

// renderWave draws a Braille-character oscilloscope waveform from raw audio samples.
// Each Braille character covers a 2×4 dot grid, giving smooth sub-cell resolution.
func (v *Visualizer) renderWave() string {
	height := v.Rows
	charCols := PanelWidth
	dotRows := height * 4
	dotCols := charCols * 2

	samples := v.waveBuf
	n := len(samples)

	// Downsample audio to one y-position per horizontal dot column.
	if cap(v.waveYBuf) >= dotCols {
		v.waveYBuf = v.waveYBuf[:dotCols]
	} else {
		v.waveYBuf = make([]int, dotCols)
	}
	ypos := v.waveYBuf
	for x := range dotCols {
		var sample float64
		if n > 0 {
			idx := x * n / dotCols
			if idx >= n {
				idx = n - 1
			}
			sample = samples[idx]
		}
		// Map sample [-1, 1] to dot row [0, dotRows-1]; center is dotRows/2.
		y := int((1.0 - sample) * float64(dotRows-1) / 2.0)
		ypos[x] = max(0, min(dotRows-1, y))
	}

	lines := make([]string, height)
	for row := range height {
		var content strings.Builder
		dotRowStart := row * 4

		for ch := range charCols {
			var braille rune = '\u2800'
			dotColStart := ch * 2

			for dc := range 2 {
				x := dotColStart + dc
				y := ypos[x]

				// Connect to previous point so the waveform is continuous.
				prevY := y
				if x > 0 {
					prevY = ypos[x-1]
				}
				yMin := min(y, prevY)
				yMax := max(y, prevY)

				for dr := range 4 {
					dotY := dotRowStart + dr
					if dotY >= yMin && dotY <= yMax {
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
