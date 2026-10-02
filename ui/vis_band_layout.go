package ui

// visBandLayout returns how many bands get columns in a panel cols wide and
// how many one-column gaps fit between them. The bands take priority, so a
// narrow panel keeps every band it can and drops gaps first.
func visBandLayout(totalBands, cols int) (visible, gaps int) {
	if totalBands <= 0 || cols <= 0 {
		return 0, 0
	}
	visible = min(totalBands, cols)
	gaps = min(visible-1, max(0, cols-visible))
	return visible, gaps
}

// visBandWidth returns the character width for band b in a panel cols wide.
// At narrow widths only the leading visible bands receive columns. Together
// with the gaps that bandGapAfter places, the bands fill the panel exactly.
func visBandWidth(totalBands, b, cols int) int {
	visible, gaps := visBandLayout(totalBands, cols)
	if b < 0 || b >= visible {
		return 0
	}
	bandCols := cols - gaps
	base := bandCols / visible
	extra := bandCols % visible
	if b < extra {
		return base + 1
	}
	return base
}

// bandGapAfter reports whether a one-column gap follows band b. When fewer
// gaps fit than there are slots between the visible bands, the gaps are spread
// evenly over the slots.
func bandGapAfter(totalBands, b, cols int) bool {
	visible, gaps := visBandLayout(totalBands, cols)
	slots := visible - 1
	if b < 0 || b >= slots || gaps <= 0 {
		return false
	}
	return (b+1)*gaps/slots > b*gaps/slots
}

// interpolateBandColumns builds per-column levels by interpolating between neighboring bands.
func interpolateBandColumns(bands []float64, bandCols []int) []float64 {
	totalCols := 0
	for _, width := range bandCols {
		totalCols += width
	}

	cols := make([]float64, totalCols)
	offset := 0
	for b, level := range bands {
		width := bandCols[b]
		if width <= 0 {
			continue
		}
		nextLevel := level
		if b+1 < len(bands) {
			nextLevel = bands[b+1]
		}
		for c := range width {
			t := float64(c) / float64(width)
			cols[offset+c] = level*(1-t) + nextLevel*t
		}
		offset += width
	}
	return cols
}

func sampleBandLinear(bands []float64, pos float64) float64 {
	switch len(bands) {
	case 0:
		return 0
	case 1:
		return bands[0]
	}
	if pos <= 0 {
		return bands[0]
	}
	last := float64(len(bands) - 1)
	if pos >= last {
		return bands[len(bands)-1]
	}
	idx := int(pos)
	frac := pos - float64(idx)
	return bands[idx]*(1-frac) + bands[idx+1]*frac
}

func resampleBandsLinear(bands []float64, totalCols int) []float64 {
	if totalCols <= 0 || len(bands) == 0 {
		return nil
	}
	if len(bands) == totalCols {
		out := make([]float64, len(bands))
		copy(out, bands)
		return out
	}
	out := make([]float64, totalCols)
	if totalCols == 1 {
		out[0] = sampleBandLinear(bands, float64(len(bands)-1)/2)
		return out
	}
	last := float64(len(bands) - 1)
	for col := range totalCols {
		pos := float64(col) / float64(totalCols-1) * last
		out[col] = sampleBandLinear(bands, pos)
	}
	return out
}
