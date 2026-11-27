package ui

import "strings"

// renderBinary draws streaming columns of 0s and 1s that scroll at speeds
// proportional to each band's energy. Higher energy produces more 1s (active
// data) and brighter coloring, creating a raw data-stream aesthetic.
func (v *Visualizer) renderBinary(bands []float64) string {
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

				// Scroll speed per column: higher energy = faster data flow.
				speed := max(1, 4-int(energy*3))
				scroll := int(v.frame) / speed

				// Bit value from position hash (time-independent; scroll creates motion).
				h := scatterHash(b, row+scroll, col, 0)
				oneProb := energy*0.6 + 0.15

				var ch byte
				if h < oneProb {
					ch = '1'
				} else {
					ch = '0'
				}

				// 1s on high-energy bands glow bright; 0s stay dim.
				var newTag int
				if ch == '1' && energy > 0.4 {
					newTag = 2
				} else if ch == '1' || energy > 0.3 {
					newTag = 1
				} else {
					newTag = 0
				}
				if newTag != tag {
					tag = newTag
					w.setTag(tag)
				}
				w.writeByte(ch)
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
