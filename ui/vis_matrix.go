package ui

import "strings"

// Half-width katakana + digits for the iconic Matrix digital rain.
var matrixChars = []rune{
	'ｦ', 'ｧ', 'ｨ', 'ｩ', 'ｪ', 'ｫ', 'ｬ', 'ｭ', 'ｮ', 'ｯ',
	'ｰ', 'ｱ', 'ｲ', 'ｳ', 'ｴ', 'ｵ', 'ｶ', 'ｷ', 'ｸ', 'ｹ', 'ｺ',
	'ｻ', 'ｼ', 'ｽ', 'ｾ', 'ｿ', 'ﾀ', 'ﾁ', 'ﾂ', 'ﾃ', 'ﾄ',
	'0', '1', '2', '3', '4', '5', '6', '7', '8', '9',
}

// renderMatrix draws falling character streams reminiscent of the Matrix
// digital rain. Each column has a fixed fall speed derived from its position.
// Band energy controls how many columns are active (rain density).
func (v *Visualizer) renderMatrix(bands []float64) string {
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
				seed := uint64(col)*7919 + 104729

				// Column activity: stable gate, changes every ~20 frames.
				// Higher energy activates more columns.
				if scatterHash(b, 0, col, v.frame/20) > energy*1.5+0.1 {
					if -1 != tag {
						flushStyleRun(&sb, &run, tag)
						tag = -1
					}
					run.WriteByte(' ')
					col++
					continue
				}

				// Fixed speed per column (2-4 frames per row step), derived
				// from column position so each drop falls steadily.
				speed := 2 + int(seed%3)

				// Trail length: 3-5 characters.
				trailLen := 3 + int((seed/7)%3)

				// Cycle length large enough for a visible gap between drops.
				cycleLen := height + trailLen + 4
				offset := int((seed / 13) % uint64(cycleLen))
				pos := (int(v.frame)/speed + offset) % cycleLen

				dist := pos - row
				if dist < 0 || dist > trailLen {
					if -1 != tag {
						flushStyleRun(&sb, &run, tag)
						tag = -1
					}
					run.WriteByte(' ')
				} else {
					// Character mutates slowly (~every 4 frames).
					charSeed := seed ^ (uint64(row)*31 + (v.frame/4)*17)
					ch := matrixChars[charSeed%uint64(len(matrixChars))]
					var newTag int
					switch {
					case dist == 0:
						newTag = 2
					case dist <= 2:
						newTag = 1
					default:
						newTag = 0
					}
					if newTag != tag {
						flushStyleRun(&sb, &run, tag)
						tag = newTag
					}
					run.WriteRune(ch)
				}
				col++
			}
			if b < bandCount-1 {
				if -1 != tag {
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
