package ui

import "strings"

// renderColumns draws many thin single-character-wide columns, interpolating
// between bands so adjacent columns vary slightly for a dense, organic look.
func (v *Visualizer) renderColumns(bands []float64) string {
	height := v.Rows
	bandCount := len(bands)
	width := v.columns()

	// Reuse per-band column counts and flat interpolated display columns.
	if cap(v.bandColsBuf) < bandCount {
		v.bandColsBuf = make([]int, bandCount)
	} else {
		v.bandColsBuf = v.bandColsBuf[:bandCount]
	}
	bandCols := v.bandColsBuf
	for b := range bandCount {
		bandCols[b] = visBandWidth(bandCount, b, width)
	}

	v.interpColsBuf = interpolateBandColumnsInto(v.interpColsBuf, bands, bandCols)
	cols := v.interpColsBuf

	var sb strings.Builder
	sb.Grow(height * (width*3 + 24))

	for row := range height {
		if row > 0 {
			sb.WriteByte('\n')
		}
		rowBottom := float64(height-1-row) / float64(height)
		rowTop := float64(height-row) / float64(height)
		style := specANSI[specTag(rowBottom)]
		if style.prefix != "" {
			sb.WriteString(style.prefix)
		}
		offset := 0

		for b := range bandCount {
			for c := range bandCols[b] {
				level := cols[offset+c]
				sb.WriteString(fracBlock(level, rowBottom, rowTop))
			}
			offset += bandCols[b]
			if bandGapAfter(bandCount, b, width) {
				sb.WriteByte(' ')
			}
		}
		if style.prefix != "" {
			sb.WriteString(style.suffix)
		}
	}

	return sb.String()
}
