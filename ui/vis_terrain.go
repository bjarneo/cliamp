package ui

import (
	"strings"
	"time"
)

// terrainDriver draws a scrolling side-view landscape where terrain height
// is the current spectrum energy. New data enters from the right and scrolls
// left, creating a moving mountain range silhouette. Braille dots give smooth
// sub-cell edges; spectrum coloring paints green valleys, yellow slopes, red peaks.
type terrainDriver struct {
	buf []float64
}

func newTerrainDriver() visModeDriver {
	return &terrainDriver{}
}

func (*terrainDriver) AnalysisSpec(*Visualizer) visAnalysisSpec {
	return spectrumAnalysisSpec(defaultSpectrumBands)
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
	dotRows := height * 4
	dotCols := panelWidth * 2
	buf := resizeTerrainBuf(d.buf, dotCols)

	// Render: each dot column is filled from its terrain height down to the bottom.
	lines := make([]string, height)
	for row := range height {
		var content strings.Builder
		for ch := range panelWidth {
			var braille rune = '\u2800'
			for dc := range 2 {
				x := ch*2 + dc
				terrainH := buf[x]
				// Top dot position — invert so 0 is bottom.
				topDot := dotRows - 1 - int(terrainH*float64(dotRows-1))
				for dr := range 4 {
					dotY := row*4 + dr
					if dotY >= topDot {
						braille |= brailleBit[dr][dc]
					}
				}
			}
			content.WriteRune(braille)
		}
		// Color by row height: green base, yellow middle, red peaks.
		lines[row] = specStyle(float64(height-1-row) / float64(height)).Render(content.String())
	}

	return strings.Join(lines, "\n")
}

func (d *terrainDriver) Tick(v *Visualizer, ctx visTickContext) {
	defaultDriverTick(v, ctx, d.AnalysisSpec(v))
	if ctx.OverlayActive {
		return
	}

	dotCols := panelWidth * 2
	d.buf = resizeTerrainBuf(d.buf, dotCols)
	if len(d.buf) < 2 {
		return
	}

	// Scroll left by 2 dot columns per frame for visible movement.
	copy(d.buf, d.buf[2:])

	// Compute new rightmost height from average spectrum energy.
	var totalEnergy float64
	for _, e := range v.bands {
		totalEnergy += e
	}
	avg := totalEnergy / float64(len(v.bands))

	// Two new columns with slight noise for organic ridge edges.
	d.buf[dotCols-2] = min(1.0, avg+scatterHash(0, 0, 0, v.frame)*0.12)
	d.buf[dotCols-1] = min(1.0, avg+scatterHash(0, 0, 1, v.frame)*0.12)
}

func (*terrainDriver) TickInterval(_ *Visualizer, ctx visTickContext) time.Duration {
	return defaultDriverTickInterval(ctx)
}

func (*terrainDriver) OnEnter(*Visualizer) {}

func (*terrainDriver) OnLeave(*Visualizer) {}
