package ui

import (
	"math"
	"strings"
)

// logoGlyphs holds 5×7 pixel bitmaps for each letter in "CLIAMP".
// Each row is 5 bits wide; bit 4 (0x10) is the leftmost pixel.
var logoGlyphs = [6][7]uint8{
	{0x0E, 0x10, 0x10, 0x10, 0x10, 0x10, 0x0E}, // C
	{0x10, 0x10, 0x10, 0x10, 0x10, 0x10, 0x1F}, // L
	{0x1F, 0x04, 0x04, 0x04, 0x04, 0x04, 0x1F}, // I
	{0x0E, 0x11, 0x11, 0x1F, 0x11, 0x11, 0x11}, // A
	{0x11, 0x1B, 0x15, 0x11, 0x11, 0x11, 0x11}, // M
	{0x1E, 0x11, 0x11, 0x1E, 0x10, 0x10, 0x10}, // P
}

const (
	logoLetterW    = 5 // pixel columns per glyph
	logoLetterH    = 7 // pixel rows per glyph
	logoNumLetters = 6
	logoGap        = 2                                                       // pixel gap between letters
	logoTotalW     = logoNumLetters*logoLetterW + (logoNumLetters-1)*logoGap // 40
)

// renderLogo draws "CLIAMP" in pixel art using Braille dots. Individual dots
// within each letter appear and disappear based on the associated frequency
// band's energy — loud passages fill the text solid, silence dissolves it
// into scattered pixels. A gentle bounce and wave keep things alive.
func (v *Visualizer) renderLogo(bands []float64) string {
	height := v.Rows
	dotRows := height * 4
	dotCols := PanelWidth * 2

	grid := make([]bool, dotRows*dotCols)

	// Scale letters to fill the panel (75% of height for bounce headroom).
	scaleX := dotCols / logoTotalW
	scaleY := (dotRows * 3 / 4) / logoLetterH
	if scaleX < 1 {
		scaleX = 1
	}
	if scaleY < 1 {
		scaleY = 1
	}

	renderedW := logoTotalW * scaleX
	renderedH := logoLetterH * scaleY
	offsetX := (dotCols - renderedW) / 2
	baseOffsetY := (dotRows - renderedH) / 2

	// Map 6 letters across the 10 frequency bands.
	letterBand := [6]int{0, 2, 4, 5, 7, 9}

	for li := range logoNumLetters {
		energy := bands[letterBand[li]]

		// Gentle traveling wave for life during silence + subtle bounce.
		wave := math.Sin(float64(v.frame)*0.06+float64(li)*0.9) * 1.5
		bounce := int(energy*float64(baseOffsetY)*0.3 + wave)

		letterX := offsetX + li*(logoLetterW+logoGap)*scaleX
		letterY := baseOffsetY - bounce

		for py := range logoLetterH {
			row := logoGlyphs[li][py]
			for px := range logoLetterW {
				if row&(1<<(logoLetterW-1-px)) == 0 {
					continue
				}

				// Stamp each glyph pixel as a scaled block of dots.
				// Each dot's visibility is gated by energy — loud fills
				// the text solid, silence dissolves it to scattered pixels.
				fill := energy*energy*0.75 + 0.15
				for sy := range scaleY {
					for sx := range scaleX {
						dx := letterX + px*scaleX + sx
						dy := letterY + py*scaleY + sy
						if dx < 0 || dx >= dotCols || dy < 0 || dy >= dotRows {
							continue
						}

						if scatterHash(li, py*scaleY+sy, px*scaleX+sx, v.frame) > fill {
							continue
						}

						grid[dy*dotCols+dx] = true
					}
				}
			}
		}
	}

	// Convert dot grid to Braille characters.
	lines := make([]string, height)
	for row := range height {
		var content strings.Builder
		for ch := range PanelWidth {
			var braille rune = '\u2800'
			for dr := range 4 {
				for dc := range 2 {
					if grid[(row*4+dr)*dotCols+ch*2+dc] {
						braille |= brailleBit[dr][dc]
					}
				}
			}
			content.WriteRune(braille)
		}
		lines[row] = specStyle(float64(height-1-row) / float64(height)).Render(content.String())
	}

	return strings.Join(lines, "\n")
}
