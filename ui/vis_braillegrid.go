package ui

import "strings"

// brailleBit maps (row, col) in a 4×2 Braille dot grid to its bit value.
var brailleBit = [4][2]rune{
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
		for col := range cols {
			var braille rune = '⠀'
			var cellTier int8
			colOff := baseRow + col*2
			for dr := range 4 {
				rOff := colOff + dr*dotCols
				if t := cells[rOff]; t != 0 {
					braille |= brailleBit[dr][0]
					if t > cellTier {
						cellTier = t
					}
				}
				if t := cells[rOff+1]; t != 0 {
					braille |= brailleBit[dr][1]
					if t > cellTier {
						cellTier = t
					}
				}
			}
			if cellTier == 0 && !g.keepRunOnEmpty {
				cellTier = 1
			}
			if cellTier != 0 && int(cellTier) != tier {
				tier = int(cellTier)
				w.switchStyle(styleFor(tier))
			}
			w.writeRune(braille)
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
		for col := range cols {
			var braille rune = '\u2800'
			colOff := baseRow + col*2
			for dr := range 4 {
				rOff := colOff + dr*dotCols
				if dots[rOff] {
					braille |= brailleBit[dr][0]
				}
				if dots[rOff+1] {
					braille |= brailleBit[dr][1]
				}
			}
			sb.WriteRune(braille)
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
