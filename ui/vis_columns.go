package ui

import "strings"

// renderColumns draws many thin single-character-wide columns, interpolating
// between bands so adjacent columns vary slightly for a dense, organic look.
func (v *Visualizer) renderColumns(bands []float64) string {
	height := v.Rows
	bandCount := len(bands)
	width := v.columns()

	// Compute per-band column counts; cols below is a flat level per display column.
	bandCols := make([]int, bandCount)
	for b := range bandCount {
		bandCols[b] = visBandWidth(bandCount, b, width)
	}

	cols := interpolateBandColumns(bands, bandCols)
	lines := make([]string, height)

	for row := range height {
		var content strings.Builder
		rowBottom := float64(height-1-row) / float64(height)
		rowTop := float64(height-row) / float64(height)
		offset := 0

		for b := range bandCount {
			for c := range bandCols[b] {
				level := cols[offset+c]
				content.WriteString(fracBlock(level, rowBottom, rowTop))
			}
			offset += bandCols[b]
			if bandGapAfter(bandCount, b, width) {
				content.WriteByte(' ')
			}
		}
		lines[row] = specWrap(rowBottom, content.String())
	}

	return strings.Join(lines, "\n")
}
