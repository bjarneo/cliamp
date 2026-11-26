package ui

import "strings"

// renderRain draws bar-shaped columns filled with falling rain streaks.
// The bar height follows band level, but the interior is animated falling drops
// with head/body/tail coloring. Higher energy makes taller, denser rainfall.
func (v *Visualizer) renderRain(bands []float64) string {
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
		curTag := -1
		w.setTag(-1)
		col := 0

		for b := range bandCount {
			bw := visBandWidth(bandCount, b, width)
			level := bands[b]
			rowNorm := float64(height-1-row) / float64(height)

			for range bw {
				if rowNorm >= level {
					if curTag != -1 {
						curTag = -1
						w.setTag(-1)
					}
					w.writeByte(' ')
					col++
					continue
				}

				seed := uint64(col)*7919 + 104729

				if scatterHash(b, 0, col, v.frame/12) > level*1.6+0.1 {
					if curTag != -1 {
						curTag = -1
						w.setTag(-1)
					}
					w.writeByte(' ')
					col++
					continue
				}

				speed := 1 + int(seed%3)
				dropLen := 2 + int((seed/7)%3)
				cycleLen := height + dropLen + 3
				offset := int((seed / 13) % uint64(cycleLen))
				pos := (int(v.frame)/speed + offset) % cycleLen

				dist := pos - row
				if dist >= 0 && dist < dropLen {
					var ch rune
					var newTag int
					switch {
					case dist == 0:
						ch = '┃'
						newTag = 2
					case dist == 1:
						ch = '│'
						newTag = 1
					default:
						ch = ':'
						newTag = 0
					}
					if newTag != curTag {
						curTag = newTag
						w.setTag(curTag)
					}
					w.writeRune(ch)
				} else {
					if curTag != -1 {
						curTag = -1
						w.setTag(-1)
					}
					w.writeByte(' ')
				}
				col++
			}
			if bandGapAfter(bandCount, b, width) {
				if curTag != -1 {
					curTag = -1
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
