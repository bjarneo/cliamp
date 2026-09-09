package ui

import (
	"strings"
	"time"
)

// mosaicDriver renders a static heatmap of small tiles. The grid never
// scrolls: each tile sits in a fixed (row, column) position and lights up or
// fades in place. Each cell is wired at startup to one frequency band and a
// personal ignition threshold, so loud passages light up many tiles at once
// while quiet passages light only the most-sensitive ones — producing a
// speckled, gradually-saturating pattern that tracks the music.
type mosaicDriver struct {
	rows, tiles int
	cells       []mosaicCellState
	rng         uint64
}

type mosaicCellState struct {
	bandIdx   int     // which spectrum band this cell listens to
	threshold float64 // band level required to ignite this cell
	value     float64 // current displayed intensity, decays each tick
}

func newMosaicDriver() visModeDriver {
	return &mosaicDriver{rng: 0xC1AB1A1015D5}
}

func (*mosaicDriver) AnalysisSpec(*Visualizer) VisAnalysisSpec {
	return spectrumAnalysisSpec(DefaultSpectrumBands)
}

const (
	mosaicCellW   = 2 // characters per tile
	mosaicCellGap = 1 // characters between tiles
	mosaicDecay   = 0.88
)

// mosaicLevel is one of the discrete brightness tiers a tile can show. Empty
// cells render as plain spaces so unlit positions disappear into the
// background, leaving only the active tiles visible.
type mosaicLevel struct {
	glyph rune
	tier  int // 0 = green, 1 = yellow, 2 = red, -1 = no color
}

var mosaicLevels = []mosaicLevel{
	{' ', -1},
	{'░', 0},
	{'▒', 0},
	{'▓', 0},
	{'█', 0},
	{'█', 1}, // hot
	{'█', 2}, // overdrive
}

func mosaicLevelFor(intensity float64) mosaicLevel {
	switch {
	case intensity >= 0.85:
		return mosaicLevels[6]
	case intensity >= 0.65:
		return mosaicLevels[5]
	case intensity >= 0.45:
		return mosaicLevels[4]
	case intensity >= 0.28:
		return mosaicLevels[3]
	case intensity >= 0.15:
		return mosaicLevels[2]
	case intensity >= 0.05:
		return mosaicLevels[1]
	default:
		return mosaicLevels[0]
	}
}

// mosaicTileCount returns how many tiles fit horizontally in panelWidth.
func mosaicTileCount(panelWidth int) int {
	step := mosaicCellW + mosaicCellGap
	if panelWidth < mosaicCellW {
		return 0
	}
	// The last tile needs no trailing gap.
	return (panelWidth + mosaicCellGap) / step
}

func (d *mosaicDriver) ensureGrid(rows, tiles, bandCount int) {
	if rows == d.rows && tiles == d.tiles && len(d.cells) == rows*tiles {
		return
	}
	d.rows = rows
	d.tiles = tiles
	d.cells = make([]mosaicCellState, rows*tiles)
	if bandCount <= 0 {
		bandCount = DefaultSpectrumBands
	}

	// Each cell picks a band biased toward its row (top → treble, bottom →
	// bass) plus a small jitter so neighbors don't share the same band, and a
	// per-cell threshold drawn from [0.04, 0.78] so the lit-cell density rises
	// naturally with loudness — that's what produces the scattered look.
	d.rng = 0xC1AB1A1015D5
	for r := 0; r < rows; r++ {
		baseBand := bandCount / 2
		if rows > 1 {
			baseBand = (rows - 1 - r) * (bandCount - 1) / (rows - 1)
		}
		for c := 0; c < tiles; c++ {
			d.rng = d.rng*6364136223846793005 + 1442695040888963407
			jitter := int((d.rng>>33)%5) - 2 // -2..+2
			band := baseBand + jitter
			if band < 0 {
				band = 0
			}
			if band >= bandCount {
				band = bandCount - 1
			}
			d.rng = d.rng*6364136223846793005 + 1442695040888963407
			th := 0.04 + float64((d.rng>>33)%1000)/1000.0*0.74
			d.cells[r*tiles+c] = mosaicCellState{
				bandIdx:   band,
				threshold: th,
				value:     0,
			}
		}
	}
}

func (d *mosaicDriver) Render(v *Visualizer) string {
	rows := v.Rows
	tiles := mosaicTileCount(PanelWidth)
	if rows <= 0 || tiles <= 0 {
		return strings.Repeat("\n", max(0, rows-1))
	}
	d.ensureGrid(rows, tiles, len(v.SmoothedBands()))

	lines := make([]string, rows)
	for row := 0; row < rows; row++ {
		var sb, run strings.Builder
		tag := -1

		for t := 0; t < tiles; t++ {
			cell := d.cells[row*tiles+t]
			level := mosaicLevelFor(cell.value)
			if level.tier != tag {
				flushStyleRun(&sb, &run, tag)
				tag = level.tier
			}
			for c := 0; c < mosaicCellW; c++ {
				run.WriteRune(level.glyph)
			}
			if t < tiles-1 {
				if tag != -1 {
					flushStyleRun(&sb, &run, tag)
					tag = -1
				}
				for g := 0; g < mosaicCellGap; g++ {
					run.WriteRune(' ')
				}
			}
		}
		flushStyleRun(&sb, &run, tag)
		lines[row] = sb.String()
	}
	return strings.Join(lines, "\n")
}

func (d *mosaicDriver) Tick(v *Visualizer, ctx VisTickContext) {
	defaultDriverTick(v, ctx, d.AnalysisSpec(v))
	if ctx.OverlayActive {
		return
	}
	rows := v.Rows
	tiles := mosaicTileCount(PanelWidth)
	if rows <= 0 || tiles <= 0 {
		return
	}
	bands := v.SmoothedBands()
	d.ensureGrid(rows, tiles, len(bands))
	if len(bands) == 0 {
		// Still decay so cells don't stick lit during silence.
		for i := range d.cells {
			d.cells[i].value *= mosaicDecay
		}
		return
	}

	// For each cell: if its assigned band exceeds the cell's threshold, ignite
	// (set value to the band level — clamped to 1.05 so spikes can briefly
	// promote into the yellow/red tiers). Otherwise decay in place.
	for i := range d.cells {
		c := &d.cells[i]
		level := bands[c.bandIdx]
		if level > c.threshold {
			ignited := level
			if ignited > 1.05 {
				ignited = 1.05
			}
			if ignited > c.value {
				c.value = ignited
			}
		}
		c.value *= mosaicDecay
		if c.value < 0.001 {
			c.value = 0
		}
	}
}

func (*mosaicDriver) TickInterval(_ *Visualizer, ctx VisTickContext) time.Duration {
	return defaultDriverTickInterval(ctx)
}

func (d *mosaicDriver) decaySettled() bool {
	for _, cell := range d.cells {
		if mosaicLevelFor(cell.value).tier >= 0 {
			return false
		}
	}
	return true
}

func (d *mosaicDriver) OnEnter(*Visualizer) {
	// Force the grid to be regenerated on next Render/Tick so each visit
	// reshuffles thresholds and band assignments — keeps the visualizer fresh.
	d.cells = nil
	d.rows = 0
	d.tiles = 0
}

func (*mosaicDriver) OnLeave(*Visualizer) {}
