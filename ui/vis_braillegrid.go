package ui

import "strings"

// brailleBit maps each row and column in a 4x2 Braille grid to its bit mask.
var brailleBit = [4][2]byte{
	{0x01, 0x08}, // row 0
	{0x02, 0x10}, // row 1
	{0x04, 0x20}, // row 2
	{0x40, 0x80}, // row 3
}

// brailleGrid is a 4×2 dot-per-cell rasteriser shared by visualizers that draw
// to a fine subgrid (mirror, heartbeat, firefly, geyser, sand, red sector).
// Each cell stores a tier (0 = empty) and the renderer composes one Braille
// glyph per character cell. A cell wears the highest tier of its eight dots.
type brailleGrid struct {
	cells   []int8
	dotRows int
	dotCols int
	// styleFor returns the ANSI style for a grid tier. Nil uses spectrumTierStyle.
	styleFor func(tier int) styleANSI
	// keepRunOnEmpty lets a blank cell join the colour that runs. A blank
	// glyph paints nothing, so a break there only adds ANSI noise. Otherwise
	// a blank cell takes the lowest tier.
	keepRunOnEmpty bool
}

// resize sizes the grid to rows by cols dots. It keeps the cells when the size
// does not change, for a caller whose grid holds state across frames.
func (g *brailleGrid) resize(rows, cols int) {
	need := rows * cols
	if rows == g.dotRows && cols == g.dotCols && len(g.cells) == need {
		return
	}
	if cap(g.cells) >= need {
		g.cells = g.cells[:need]
		clear(g.cells)
	} else {
		g.cells = make([]int8, need)
	}
	g.dotRows = rows
	g.dotCols = cols
}

// ensure sizes the grid and clears it for a new frame.
func (g *brailleGrid) ensure(rows, cols int) {
	g.resize(rows, cols)
	g.clear()
}

func (g *brailleGrid) clear() {
	clear(g.cells)
}

func (g *brailleGrid) set(x, y int, tier int8) {
	if x < 0 || x >= g.dotCols || y < 0 || y >= g.dotRows {
		return
	}
	if tier > g.cells[y*g.dotCols+x] {
		g.cells[y*g.dotCols+x] = tier
	}
}

// render flattens the dot grid to rows lines of cols glyphs, packing 4×2 dot
// blocks into Braille glyphs and emitting tier-coloured runs.
func (g *brailleGrid) render(rows, cols int) string {
	if g.dotRows < rows*4 || g.dotCols < cols*2 {
		return strings.Repeat("\n", max(0, rows-1))
	}
	styleFor := g.styleFor
	if styleFor == nil {
		styleFor = spectrumTierStyle
	}
	var sb strings.Builder
	sb.Grow(rows * (cols*6 + 16))
	w := styleRunWriter{sb: &sb}
	dotCols := g.dotCols
	cells := g.cells
	for row := range rows {
		if row > 0 {
			sb.WriteByte('\n')
		}
		tier := 0
		w.switchStyle(styleFor(0))
		baseRow := row * 4 * dotCols
		span := cols * 2
		if span > 0 {
			r0 := cells[baseRow : baseRow+span]
			r1 := cells[baseRow+dotCols : baseRow+dotCols+span]
			r2 := cells[baseRow+2*dotCols : baseRow+2*dotCols+span]
			r3 := cells[baseRow+3*dotCols : baseRow+3*dotCols+span]
			for x := 0; x < span; x += 2 {
				t00, t01 := r0[x], r0[x+1]
				t10, t11 := r1[x], r1[x+1]
				t20, t21 := r2[x], r2[x+1]
				t30, t31 := r3[x], r3[x+1]
				if (t00 | t01 | t10 | t11 | t20 | t21 | t30 | t31) == 0 {
					if !g.keepRunOnEmpty && tier != 1 {
						tier = 1
						w.switchStyle(styleFor(1))
					}
					w.writeBraille(0)
					continue
				}
				var mask byte
				var cellTier int8
				if t00 != 0 {
					mask |= 0x01
					if t00 > cellTier {
						cellTier = t00
					}
				}
				if t01 != 0 {
					mask |= 0x08
					if t01 > cellTier {
						cellTier = t01
					}
				}
				if t10 != 0 {
					mask |= 0x02
					if t10 > cellTier {
						cellTier = t10
					}
				}
				if t11 != 0 {
					mask |= 0x10
					if t11 > cellTier {
						cellTier = t11
					}
				}
				if t20 != 0 {
					mask |= 0x04
					if t20 > cellTier {
						cellTier = t20
					}
				}
				if t21 != 0 {
					mask |= 0x20
					if t21 > cellTier {
						cellTier = t21
					}
				}
				if t30 != 0 {
					mask |= 0x40
					if t30 > cellTier {
						cellTier = t30
					}
				}
				if t31 != 0 {
					mask |= 0x80
					if t31 > cellTier {
						cellTier = t31
					}
				}
				if cellTier == 0 && !g.keepRunOnEmpty {
					cellTier = 1
				}
				if cellTier != 0 && int(cellTier) != tier {
					tier = int(cellTier)
					w.switchStyle(styleFor(tier))
				}
				w.writeBraille(mask)
			}
		}
		w.flush()
	}
	return sb.String()
}

// spectrumTierStyle maps grid tiers 1, 2, and 3 to the low, mid, and high
// spectrum styles. Tier 0 returns an unstyled entry.
func spectrumTierStyle(tier int) styleANSI {
	return specStyle(tier - 1)
}

// packBraille packs a dot mask of rows*4 by cols*2 dots, dotCols wide, into
// rows lines of Braille glyphs. rowLevel picks the spectrum colour of each
// whole line, as specWrap does.
func packBraille(dots []bool, dotCols, rows, cols int, rowLevel func(row, rows int) float64) string {
	if rows <= 0 {
		return ""
	}
	var sb strings.Builder
	sb.Grow(rows * (cols*3 + 24))
	for row := range rows {
		if row > 0 {
			sb.WriteByte('\n')
		}
		style := specANSI[specTag(rowLevel(row, rows))]
		if style.prefix != "" {
			sb.WriteString(style.prefix)
		}
		baseRow := row * 4 * dotCols
		span := cols * 2
		if span > 0 {
			r0 := dots[baseRow : baseRow+span]
			r1 := dots[baseRow+dotCols : baseRow+dotCols+span]
			r2 := dots[baseRow+2*dotCols : baseRow+2*dotCols+span]
			r3 := dots[baseRow+3*dotCols : baseRow+3*dotCols+span]
			for x := 0; x < span; x += 2 {
				var mask byte
				if r0[x] {
					mask |= 0x01
				}
				if r0[x+1] {
					mask |= 0x08
				}
				if r1[x] {
					mask |= 0x02
				}
				if r1[x+1] {
					mask |= 0x10
				}
				if r2[x] {
					mask |= 0x04
				}
				if r2[x+1] {
					mask |= 0x20
				}
				if r3[x] {
					mask |= 0x40
				}
				if r3[x+1] {
					mask |= 0x80
				}
				sb.WriteString(brailleUTF8[mask])
			}
		}
		if style.prefix != "" {
			sb.WriteString(style.suffix)
		}
	}
	return sb.String()
}

// specRowLevel is the usual spectrum gradient: the low colour on the bottom
// row and the high colour towards the top.
func specRowLevel(row, rows int) float64 {
	return float64(rows-1-row) / float64(rows)
}

// dotMaskFor returns the per-frame dot mask that the Braille modes share,
// cleared and sized to n dots. Only one mode renders a frame, so one buffer
// serves all of them.
func (v *Visualizer) dotMaskFor(n int) []bool {
	if cap(v.dotMask) < n {
		v.dotMask = make([]bool, n)
	} else {
		v.dotMask = v.dotMask[:n]
		clear(v.dotMask)
	}
	return v.dotMask
}

// lcgNext advances a 64-bit LCG and returns the top 31 bits of the new state.
// Every visualizer that needs a repeatable random stream draws from it.
func lcgNext(state *uint64) uint64 {
	*state = *state*6364136223846793005 + 1442695040888963407
	return *state >> 33
}

// rng64 advances a 64-bit LCG and returns a [0,1) double.
func rng64(state *uint64) float64 {
	return float64(lcgNext(state)%1000) / 1000.0
}
