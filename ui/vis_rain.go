package ui

import "strings"

// renderRain draws bar-shaped columns filled with falling rain streaks.
// The bar height follows band level, but the interior is animated falling drops
// with head/body/tail coloring. Higher energy makes taller, denser rainfall.
func (v *Visualizer) renderRain(bands []float64) string {
	height := v.Rows
	lines := make([]string, height)
	bandCount := len(bands)

	for row := range height {
		var sb, run strings.Builder
		curTag := -1
		col := 0

		for b := range bandCount {
			bw := visBandWidth(bandCount, b)
			level := bands[b]
			rowNorm := float64(height-1-row) / float64(height)

			for range bw {
				if rowNorm >= level {
					// Above the bar — empty.
					if curTag != -1 {
						flushStyleRun(&sb, &run, curTag)
						curTag = -1
					}
					run.WriteByte(' ')
					col++
					continue
				}

				// Inside the bar — check if this column is active.
				seed := uint64(col)*7919 + 104729

				// Column activation: gate changes slowly, energy controls density.
				if scatterHash(b, 0, col, v.frame/12) > level*1.6+0.1 {
					if curTag != -1 {
						flushStyleRun(&sb, &run, curTag)
						curTag = -1
					}
					run.WriteByte(' ')
					col++
					continue
				}

				// Per-column fall speed: 1-3 frames per row step.
				speed := 1 + int(seed%3)

				// Drop length: 2-4 characters.
				dropLen := 2 + int((seed/7)%3)

				// Cycle through visible height with gap before repeating.
				cycleLen := height + dropLen + 3
				offset := int((seed / 13) % uint64(cycleLen))
				pos := (int(v.frame)/speed + offset) % cycleLen

				dist := pos - row
				if dist >= 0 && dist < dropLen {
					// Pick character: head gets │, body gets thinner chars.
					var ch rune
					switch {
					case dist == 0:
						ch = '┃'
					case dist == 1:
						ch = '│'
					default:
						ch = ':'
					}

					// Color by drop position: bright head, mid body, dim tail.
					var newTag int
					switch {
					case dist == 0:
						newTag = 2
					case dist == 1:
						newTag = 1
					default:
						newTag = 0
					}
					if newTag != curTag {
						flushStyleRun(&sb, &run, curTag)
						curTag = newTag
					}
					run.WriteRune(ch)
				} else {
					// Empty space between drops.
					if curTag != -1 {
						flushStyleRun(&sb, &run, curTag)
						curTag = -1
					}
					run.WriteByte(' ')
				}
				col++
			}
			if b < bandCount-1 {
				if curTag != -1 {
					flushStyleRun(&sb, &run, curTag)
					curTag = -1
				}
				run.WriteByte(' ')
				col++
			}
		}
		flushStyleRun(&sb, &run, curTag)
		lines[row] = sb.String()
	}

	return strings.Join(lines, "\n")
}
