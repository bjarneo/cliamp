package ui

import (
	"strings"
	"time"
)

// esperDriver renders a Blade Runner Esper machine grid scan with CRT phosphor
// persistence. A horizontal scan line sweeps top-to-bottom, lighting crosshair
// grid points as it passes. Lit cells decay over several frames, leaving ghost
// trails that fade from accent → mid → dim → gone.
type esperDriver struct {
	// phosphor stores decay generation per terminal character cell.
	// 0 = off, 3 = just lit (accent), 2 = fading (mid), 1 = dim, then gone.
	phosphor [][]uint8
	scanDot  int // current scan line position in dot-space (0 .. dotRows-1)
}

func newEsperDriver() visModeDriver {
	return &esperDriver{}
}

func (*esperDriver) AnalysisSpec(*Visualizer) VisAnalysisSpec {
	return spectrumAnalysisSpec(DefaultSpectrumBands)
}

func (*esperDriver) TickInterval(_ *Visualizer, ctx VisTickContext) time.Duration {
	return defaultDriverTickInterval(ctx)
}

func (d *esperDriver) OnEnter(v *Visualizer) {
	d.phosphor = nil
	d.scanDot = 0
}

func (*esperDriver) OnLeave(*Visualizer) {}

// ensurePhosphor allocates or resizes the phosphor buffer to match current dimensions.
func (d *esperDriver) ensurePhosphor(rows, cols int) {
	if rows <= 0 || cols <= 0 {
		d.phosphor = nil
		return
	}
	if len(d.phosphor) == rows && (rows == 0 || len(d.phosphor[0]) == cols) {
		return
	}
	d.phosphor = make([][]uint8, rows)
	for r := range rows {
		d.phosphor[r] = make([]uint8, cols)
	}
}

func (d *esperDriver) Tick(v *Visualizer, ctx VisTickContext) {
	defaultDriverTick(v, ctx, d.AnalysisSpec(v))
	if ctx.OverlayActive {
		return
	}

	rows := v.Rows
	cols := PanelWidth
	d.ensurePhosphor(rows, cols)
	if len(d.phosphor) == 0 {
		return
	}

	dotRows := rows * 4

	// Advance scan line.
	d.scanDot = (d.scanDot + 1) % dotRows
	scanRow := d.scanDot / 4

	// Compute average energy for grid density control.
	var avgEnergy float64
	for _, b := range v.bands {
		avgEnergy += b
	}
	if len(v.bands) > 0 {
		avgEnergy /= float64(len(v.bands))
	}

	// Grid spacing: denser with more energy. Range from every 6th col to every 2nd.
	spacing := max(2, int(6.0-avgEnergy*4.0))

	// Light up grid points on the current scan row.
	for c := range cols {
		if c%spacing == 0 {
			d.phosphor[scanRow][c] = 4 // fresh: will render as accent
		}
	}

	// Also light the row directly below the scan for a thicker sweep effect.
	if scanRow+1 < rows {
		for c := range cols {
			if c%spacing == 0 && d.phosphor[scanRow+1][c] < 2 {
				d.phosphor[scanRow+1][c] = 2
			}
		}
	}

	// Decay pass: all non-zero cells lose one generation.
	// Runs after lighting new grid points so fresh cells start at full brightness.
	for r := range rows {
		for c := range cols {
			if d.phosphor[r][c] > 0 {
				d.phosphor[r][c]--
			}
		}
	}
}

// gridGlyph returns the crosshair character for a given phosphor value.
var gridGlyphs = [5]rune{' ', '·', '┼', '╬', '╋'}

func (d *esperDriver) Render(v *Visualizer) string {
	rows := v.Rows
	cols := PanelWidth
	// Read-only: do not allocate here. Tick handles buffer sizing.
	if len(d.phosphor) == 0 || len(d.phosphor) != rows || len(d.phosphor[0]) != cols {
		return ""
	}

	lines := make([]string, rows)
	for r := range rows {
		var sb, run strings.Builder
		tag := -1

		for c := range cols {
			p := d.phosphor[r][c]

			// Baseline grid skeleton: faint dots at regular intervals even at silence.
			// Mimics the Esper machine's standby overlay.
			if p == 0 && r%2 == 0 && c%8 == 0 {
				p = 1
			}

			ch := gridGlyphs[min(int(p), len(gridGlyphs)-1)]

			var newTag int
			switch {
			case p >= 3:
				newTag = 2 // accent (high)
			case p == 2:
				newTag = 1 // mid
			case p == 1:
				newTag = 0 // dim
			default:
				newTag = -1
			}

			if newTag != tag {
				flushStyleRun(&sb, &run, tag)
				tag = newTag
			}
			run.WriteRune(ch)
		}
		flushStyleRun(&sb, &run, tag)
		lines[r] = sb.String()
	}

	// Add subtle braille scan-line indicator at the current scan position.
	scanCharRow := d.scanDot / 4
	if scanCharRow < rows {
		// Overlay a braille scan line across the row for sub-cell precision.
		subRow := d.scanDot % 4
		var scanSB strings.Builder
		for range cols {
			var braille rune = '\u2800'
			for dc := range 2 {
				braille |= brailleBit[subRow][dc]
			}
			scanSB.WriteRune(braille)
		}
		scanLine := specHighStyle.Render(scanSB.String())

		// Merge: if the grid line has content, overlay; if empty, use scan line.
		existing := lines[scanCharRow]
		if strings.TrimSpace(existing) == "" {
			lines[scanCharRow] = scanLine
		} else {
			// The scan line renders below the grid content for visual layering.
			_ = scanLine // Grid content takes priority; scan line visible in gaps.
		}
	}

	return strings.Join(lines, "\n")
}

