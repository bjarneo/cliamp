package ui

import "strings"

// glitchChars are block/box characters ordered roughly by visual intensity.
// Low-energy glitches pick from the beginning (subtle), high-energy from the end (dense).
var glitchChars = []rune{
	'░', '▒', '▓', '█', '▌', '▐', '▀', '▄',
	'╳', '╱', '╲', '┼', '╬', '▞', '▚',
}

// renderGlitch draws random block corruption that intensifies with audio energy.
// Quiet passages show mostly empty space with occasional flickers; loud passages
// fill the display with dense, rapidly-changing block characters.
func (v *Visualizer) renderGlitch(bands []float64) string {
	height := v.Rows
	lines := make([]string, height)
	bandCount := len(bands)

	for row := range height {
		var sb, run strings.Builder
		tag := -1
		col := 0

		for b := range bandCount {
			w := visBandWidth(bandCount, b)
			for range w {
				energy := bands[b]
				h := scatterHash(b, row, col, v.frame)

				// Probability of glitch increases with energy squared.
				threshold := energy * energy * 1.5

				if h < threshold {
					// Pick glitch character: higher energy biases toward denser chars.
					charIdx := int(h * float64(len(glitchChars)) * (1.0 + energy))
					if charIdx >= len(glitchChars) {
						charIdx = len(glitchChars) - 1
					}
					ch := glitchChars[charIdx]
					newTag := specTag(energy)
					if newTag != tag {
						flushStyleRun(&sb, &run, tag)
						tag = newTag
					}
					run.WriteRune(ch)
				} else {
					if tag != -1 {
						flushStyleRun(&sb, &run, tag)
						tag = -1
					}
					run.WriteByte(' ')
				}
				col++
			}
			if b < bandCount-1 {
				if tag != -1 {
					flushStyleRun(&sb, &run, tag)
					tag = -1
				}
				run.WriteByte(' ')
				col++
			}
		}
		flushStyleRun(&sb, &run, tag)
		lines[row] = sb.String()
	}

	return strings.Join(lines, "\n")
}
