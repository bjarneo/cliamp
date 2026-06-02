package ui

import (
	"strings"
	"time"
)

// flameDriver renders a fire effect using the classic doom-fire propagation:
// a heat field is fed at the bottom row from the spectrum, then each frame
// every cell inherits its neighbour-below's heat with a small lateral wind
// jitter and a random decay. The result is a continuous, lapping flame
// instead of a row of independent columns. Bass thickens the heat source so
// loud passages feed taller flames; quiet passages settle into a low,
// flickering bed of coals.
type flameDriver struct {
	heat             []float64
	dotRows, dotCols int
	rng              uint64
	frame            uint64
}

func newFlameDriver() visModeDriver {
	return &flameDriver{rng: 0xF1A3C0DE0BADCAFE}
}

func (*flameDriver) AnalysisSpec(*Visualizer) VisAnalysisSpec {
	return spectrumAnalysisSpec(DefaultSpectrumBands)
}

func (d *flameDriver) ensure(rows, cols int) {
	if d.dotRows == rows && d.dotCols == cols && len(d.heat) == rows*cols {
		return
	}
	d.heat = make([]float64, rows*cols)
	d.dotRows = rows
	d.dotCols = cols
}

func (d *flameDriver) Tick(v *Visualizer, ctx VisTickContext) {
	defaultDriverTick(v, ctx, d.AnalysisSpec(v))
	if ctx.OverlayActive {
		return
	}

	dotRows := v.Rows * 4
	dotCols := PanelWidth * 2
	if dotRows < 4 || dotCols < 4 {
		return
	}
	d.ensure(dotRows, dotCols)
	d.frame = v.frame

	bands := v.SmoothedBands()
	bandCount := len(bands)

	// Source (bottom) row: per-column heat seeded from a smooth spectrum sample
	// plus a small per-column sparkle so the base shimmers even on quiet input.
	if bandCount > 0 {
		last := float64(bandCount - 1)
		for x := 0; x < dotCols; x++ {
			pos := float64(x) / float64(max(1, dotCols-1)) * last
			src := sampleBandLinear(bands, pos)

			d.rng = d.rng*6364136223846793005 + 1442695040888963407
			sparkle := float64((d.rng>>33)%100) / 100.0 * 0.18

			// Always keep the base mildly lit so a bed of embers is visible.
			base := 0.30 + 0.70*src + sparkle
			if base > 1.05 {
				base = 1.05
			}
			d.heat[x] = base
		}
	} else {
		for x := 0; x < dotCols; x++ {
			d.rng = d.rng*6364136223846793005 + 1442695040888963407
			d.heat[x] = 0.30 + float64((d.rng>>33)%100)/100.0*0.20
		}
	}

	// Propagate heat upward. Process top→down so we always read row y-1 before
	// any later iteration overwrites it. Each cell inherits from a horizontally
	// jittered neighbour below (the "wind") and loses a randomised amount of
	// heat — that randomness is what gives the flame its wispy texture.
	for y := dotRows - 1; y >= 1; y-- {
		// Heat decays faster toward the top so flames taper. Tuned light so
		// flames can climb most of the panel on a strong source.
		heightFrac := float64(y) / float64(max(1, dotRows-1))
		decayBase := 0.010 + 0.028*heightFrac
		for x := 0; x < dotCols; x++ {
			d.rng = d.rng*6364136223846793005 + 1442695040888963407
			r := d.rng >> 33
			offset := int(r%3) - 1 // -1, 0, +1
			r >>= 2
			decayJitter := float64(r%100) / 100.0 * 0.018
			sourceX := x + offset
			if sourceX < 0 {
				sourceX = 0
			} else if sourceX >= dotCols {
				sourceX = dotCols - 1
			}
			next := d.heat[(y-1)*dotCols+sourceX] - decayBase - decayJitter
			if next < 0 {
				next = 0
			}
			d.heat[y*dotCols+x] = next
		}
	}
}

func (*flameDriver) TickInterval(_ *Visualizer, ctx VisTickContext) time.Duration {
	return defaultDriverTickInterval(ctx)
}

func (d *flameDriver) OnEnter(v *Visualizer) {
	if v == nil {
		d.heat = nil
		d.dotRows = 0
		d.dotCols = 0
		return
	}
	d.ensure(v.Rows*4, PanelWidth*2)
	for i := range d.heat {
		d.heat[i] = 0
	}
}

func (*flameDriver) OnLeave(*Visualizer) {}

func (d *flameDriver) Render(v *Visualizer) string {
	height := v.Rows
	dotRows := height * 4
	dotCols := PanelWidth * 2
	if dotRows < 4 || dotCols < 4 {
		return strings.Repeat("\n", max(0, height-1))
	}
	if d.dotRows != dotRows || d.dotCols != dotCols || len(d.heat) != dotRows*dotCols {
		d.ensure(dotRows, dotCols)
	}

	lines := make([]string, height)
	for row := 0; row < height; row++ {
		var sb, run strings.Builder
		tag := -1
		for col := 0; col < PanelWidth; col++ {
			var braille rune = '⠀'
			cellTag := -1
			for dr := 0; dr < 4; dr++ {
				for dc := 0; dc < 2; dc++ {
					y := row*4 + dr
					x := col*2 + dc
					// Panel y=0 is top; heat buffer y=0 is bottom (the source).
					heatY := dotRows - 1 - y
					h := d.heat[heatY*dotCols+x]

					// Wispy tips: at low heat, only stochastically light the dot
					// so the upper edge of the flame has a soft, broken silhouette
					// instead of a hard cutoff.
					if h < 0.10 {
						continue
					}
					if h < 0.25 && scatterHash(0, y, x, d.frame) > h*4 {
						continue
					}

					// Tier mapping:
					//   hottest → yellow (mid spectrum tier)
					//   body    → red (high tier)
					//   tips    → red, stippled above
					var t int
					switch {
					case h >= 0.55:
						t = 1 // yellow core
					default:
						t = 2 // red body / tips
					}
					braille |= brailleBit[dr][dc]
					if t > cellTag {
						cellTag = t
					}
				}
			}
			if cellTag < 0 {
				cellTag = 0
			}
			if cellTag != tag {
				flushStyleRun(&sb, &run, tag)
				tag = cellTag
			}
			run.WriteRune(braille)
		}
		flushStyleRun(&sb, &run, tag)
		lines[row] = sb.String()
	}
	return strings.Join(lines, "\n")
}
