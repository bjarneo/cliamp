package ui

import "strings"

// brailleGrid is a 4×2 dot-per-cell rasteriser shared by visualizers that draw
// to a fine subgrid (mirror, sand, speaker, lightning, crack, quake, geyser, strings).
// Each cell stores a tier (1..3 = low/mid/high colour, 0 = empty) and the
// renderer composes one Braille glyph per character cell.
type brailleGrid struct {
	cells   []int8
	dotRows int
	dotCols int
}

func (g *brailleGrid) ensure(rows, cols int) {
	if rows == g.dotRows && cols == g.dotCols && len(g.cells) == rows*cols {
		for i := range g.cells {
			g.cells[i] = 0
		}
		return
	}
	g.cells = make([]int8, rows*cols)
	g.dotRows = rows
	g.dotCols = cols
}

func (g *brailleGrid) clear() {
	for i := range g.cells {
		g.cells[i] = 0
	}
}

func (g *brailleGrid) set(x, y int, tier int8) {
	if x < 0 || x >= g.dotCols || y < 0 || y >= g.dotRows {
		return
	}
	if tier > g.cells[y*g.dotCols+x] {
		g.cells[y*g.dotCols+x] = tier
	}
}

// render flattens the dot grid to len(rows) lines, packing 4×2 dot blocks into
// Braille glyphs and emitting tier-coloured runs.
func (g *brailleGrid) render(rows int) string {
	if g.dotRows < rows*4 || g.dotCols < PanelWidth*2 {
		return strings.Repeat("\n", max(0, rows-1))
	}
	lines := make([]string, rows)
	for row := 0; row < rows; row++ {
		var sb, run strings.Builder
		tag := -1
		for col := 0; col < PanelWidth; col++ {
			var braille rune = '⠀'
			cellTag := -1
			for dr := 0; dr < 4; dr++ {
				for dc := 0; dc < 2; dc++ {
					y := row*4 + dr
					x := col*2 + dc
					t := g.cells[y*g.dotCols+x]
					if t == 0 {
						continue
					}
					braille |= brailleBit[dr][dc]
					if int(t)-1 > cellTag {
						cellTag = int(t) - 1
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

// rng64 advances a 64-bit LCG and returns a [0,1) double.
func rng64(state *uint64) float64 {
	*state = *state*6364136223846793005 + 1442695040888963407
	return float64((*state>>33)%1000) / 1000.0
}
