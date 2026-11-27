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
	bandCount := len(bands)
	width := v.columns()

	var sb strings.Builder
	sb.Grow(height * (width*5 + 16))
	w := styleRunWriter{sb: &sb}

	for row := range height {
		if row > 0 {
			sb.WriteByte('\n')
		}
		tag := -1
		w.setTag(-1)
		col := 0
		for b := range bandCount {
			bw := visBandWidth(bandCount, b, width)
			for range bw {
				energy := bands[b]
				seed := uint64(col)*7919 + 104729

				if scatterHash(b, 0, col, v.frame/20) > energy*1.5+0.1 {
					if tag != -1 {
						tag = -1
						w.setTag(-1)
					}
					w.writeByte(' ')
					col++
					continue
				}

				speed := 2 + int(seed%3)
				trailLen := 3 + int((seed/7)%3)
				cycleLen := height + trailLen + 4
				offset := int((seed / 13) % uint64(cycleLen))
				pos := (int(v.frame)/speed + offset) % cycleLen

				dist := pos - row
				if dist < 0 || dist > trailLen {
					if tag != -1 {
						tag = -1
						w.setTag(-1)
					}
					w.writeByte(' ')
				} else {
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
						tag = newTag
						w.setTag(tag)
					}
					w.writeRune(ch)
				}
				col++
			}
			if bandGapAfter(bandCount, b, width) {
				if tag != -1 {
					tag = -1
					w.setTag(-1)
				}
				w.writeByte(' ')
				col++
			}
		}
		w.flush()
	}
	return sb.String()
}
