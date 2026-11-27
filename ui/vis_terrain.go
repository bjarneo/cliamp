package ui

import "strings"

// terrainDriver draws a scrolling side-view landscape where terrain height
// is the current spectrum energy. New data enters from the right and scrolls
// left, creating a moving mountain range silhouette. Braille dots give smooth
// sub-cell edges; spectrum coloring paints green valleys, yellow slopes, red peaks.
type terrainDriver struct {
	spectrumDriverBase

	buf []float64
}

func newTerrainDriver() visModeDriver {
	return &terrainDriver{}
}

func resizeTerrainBuf(buf []float64, dotCols int) []float64 {
	if dotCols <= 0 {
		return nil
	}
	if len(buf) == dotCols {
		return buf
	}
	// Ensure terrain buffer matches current panel width.
	next := make([]float64, dotCols)
	copyLen := min(len(buf), dotCols)
	copy(next[dotCols-copyLen:], buf[len(buf)-copyLen:])
	return next
}

func (d *terrainDriver) Render(v *Visualizer) string {
	height := v.Rows
	cols := v.columns()
	dotRows := height * 4
	dotCols := cols * 2
	buf := resizeTerrainBuf(d.buf, dotCols)

	// Fill each dot column from its terrain height down to the bottom.
	var sb strings.Builder
	sb.Grow(height * (cols*3 + 24))
	for row := range height {
		if row > 0 {
			sb.WriteByte('\n')
		}
		style := specANSI[specTag(float64(height-1-row)/float64(height))]
		if style.prefix != "" {
			sb.WriteString(style.prefix)
		}
		for ch := range cols {
			var mask byte
			for dc := range 2 {
				x := ch*2 + dc
				terrainH := buf[x]
				topDot := dotRows - 1 - int(terrainH*float64(dotRows-1))
				for dr := range 4 {
					dotY := row*4 + dr
					if dotY >= topDot {
						mask |= brailleBit[dr][dc]
					}
				}
			}
			sb.WriteString(brailleUTF8[mask])
		}
		if style.prefix != "" {
			sb.WriteString(style.suffix)
		}
	}

	return sb.String()
}

func (d *terrainDriver) Tick(v *Visualizer, ctx VisTickContext) {
	defaultDriverTick(v, ctx, d.AnalysisSpec(v))
	if ctx.OverlayActive {
		return
	}

	dotCols := v.columns() * 2
	d.buf = resizeTerrainBuf(d.buf, dotCols)
	if len(d.buf) < 2 {
		return
	}

	// Scroll left by 2 dot columns per frame for visible movement.
	copy(d.buf, d.buf[2:])

	// Compute new rightmost height from average smoothed spectrum energy
	// so successive scrolled columns glide instead of stepping at the FFT rate.
	bands := v.SmoothedBands()
	var totalEnergy float64
	for _, e := range bands {
		totalEnergy += e
	}
	avg := totalEnergy / float64(max(1, len(bands)))

	// Two new columns with slight noise for organic ridge edges.
	d.buf[dotCols-2] = min(1.0, avg+scatterHash(0, 0, 0, v.frame)*0.12)
	d.buf[dotCols-1] = min(1.0, avg+scatterHash(0, 0, 1, v.frame)*0.12)
}

func (*terrainDriver) OnEnter(*Visualizer) {}
