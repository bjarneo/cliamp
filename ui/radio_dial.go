package ui

import (
	"fmt"
	"math"
	"strconv"
	"strings"

	"charm.land/lipgloss/v2"
)

// FM and AM band boundaries.
const (
	fmMin = 87.5
	fmMax = 108.0
	amMin = 530.0
	amMax = 1700.0
)

// dialTicksFM are the major tick marks on the FM dial (labeled).
var dialTicksFM = []float64{88, 90, 92, 94, 96, 98, 100, 102, 104, 106, 108}

// dialMinorTicksFM are the minor subdivisions between major FM ticks.
var dialMinorTicksFM = func() []float64 {
	var t []float64
	for f := 89.0; f < 108.0; f += 2.0 {
		t = append(t, f)
	}
	return t
}()

// dialTicksAM are the major tick marks on the AM dial (labeled).
var dialTicksAM = []float64{600, 800, 1000, 1200, 1400, 1600}

// dialMinorTicksAM are minor subdivisions for AM.
var dialMinorTicksAM = func() []float64 {
	var t []float64
	for f := 700.0; f < 1700.0; f += 200.0 {
		t = append(t, f)
	}
	return t
}()

// ParseFrequency extracts the numeric frequency and band ("FM" or "AM") from
// a string like "88.3 FM" or "1200 AM". Returns 0, "" on parse failure.
func ParseFrequency(s string) (float64, string) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, ""
	}

	band := "FM" // default
	upper := strings.ToUpper(s)
	if strings.HasSuffix(upper, " FM") {
		s = strings.TrimSpace(s[:len(s)-3])
	} else if strings.HasSuffix(upper, " AM") {
		band = "AM"
		s = strings.TrimSpace(s[:len(s)-3])
	} else if strings.HasSuffix(upper, "FM") {
		s = strings.TrimSpace(s[:len(s)-2])
	} else if strings.HasSuffix(upper, "AM") {
		band = "AM"
		s = strings.TrimSpace(s[:len(s)-2])
	}

	freq, err := strconv.ParseFloat(s, 64)
	if err != nil || freq <= 0 {
		return 0, ""
	}

	if freq > 200 {
		band = "AM"
	}

	return freq, band
}

// dialRows is the height of the Braille dial area in terminal rows.
const dialRows = 4

// Dial colors: green scale with a red needle, like a 1970s Sansui/Marantz.
var (
	dialGreen    = lipgloss.ANSIColor(2)  // dark green — warm backlit scale
	dialGreenLit = lipgloss.ANSIColor(10) // bright green — lit numerals/ticks
	dialRed      = lipgloss.ANSIColor(9)  // bright red — the needle/pointer
)

// Grid cell types for the two-color render pass.
const (
	cellEmpty    = 0
	cellGreenDim = 1 // dim green (untuned band fill)
	cellGreen    = 2 // bright green (tuned band fill, ticks)
	cellRed      = 3 // red needle
)

// RenderRadioDial draws a retro receiver-style frequency dial. Green backlit
// scale with a thin red pointer line, inspired by 1970s Sansui and Marantz
// stereo receivers. The band fills bright green left of the needle and dim
// green right. Major and minor tick marks punctuate the scale.
func RenderRadioDial(freqStr string, width int) string {
	freq, band := ParseFrequency(freqStr)
	if freq == 0 {
		return ""
	}

	if width < 30 {
		width = 30
	}

	var minF, maxF float64
	var majorTicks, minorTicks []float64
	if band == "AM" {
		minF, maxF = amMin, amMax
		majorTicks, minorTicks = dialTicksAM, dialMinorTicksAM
	} else {
		minF, maxF = fmMin, fmMax
		majorTicks, minorTicks = dialTicksFM, dialMinorTicksFM
	}

	pos := clampF(freq, minF, maxF)

	dotCols := width * 2
	height := dialRows
	dotRows := height * 4

	needleDot := int(math.Round(float64(dotCols-1) * (pos - minF) / (maxF - minF)))

	grid := make([]byte, dotRows*dotCols)

	// --- Filled band: bottom 40% solid green strip ---
	// Left of needle = bright green, right = dim green.
	bandHeight := dotRows * 2 / 5
	if bandHeight < 4 {
		bandHeight = 4
	}
	for dr := 0; dr < bandHeight; dr++ {
		row := dotRows - 1 - dr
		for dc := range dotCols {
			if dc <= needleDot {
				grid[row*dotCols+dc] = cellGreen
			} else {
				grid[row*dotCols+dc] = cellGreenDim
			}
		}
	}

	// --- Major tick marks: tall columns through the band and above ---
	majorTickHeight := bandHeight + 6
	for _, t := range majorTicks {
		tc := int(math.Round(float64(dotCols-1) * (t - minF) / (maxF - minF)))
		if tc < 0 || tc >= dotCols {
			continue
		}
		for dr := 0; dr < majorTickHeight && dotRows-1-dr >= 0; dr++ {
			row := dotRows - 1 - dr
			brightness := byte(cellGreenDim)
			if tc <= needleDot {
				brightness = cellGreen
			}
			if grid[row*dotCols+tc] < brightness {
				grid[row*dotCols+tc] = brightness
			}
		}
	}

	// --- Minor tick marks: shorter columns, just above the band ---
	minorTickHeight := bandHeight + 3
	for _, t := range minorTicks {
		tc := int(math.Round(float64(dotCols-1) * (t - minF) / (maxF - minF)))
		if tc < 0 || tc >= dotCols {
			continue
		}
		for dr := 0; dr < minorTickHeight && dotRows-1-dr >= 0; dr++ {
			row := dotRows - 1 - dr
			brightness := byte(cellGreenDim)
			if tc <= needleDot {
				brightness = cellGreen
			}
			if grid[row*dotCols+tc] < brightness {
				grid[row*dotCols+tc] = brightness
			}
		}
	}

	// --- Red needle: exactly 2-dot-wide line from bottom to top ---
	// We paint two adjacent dot-columns as red. To prevent the needle from
	// appearing wider in the filled band, clear any green dots in the same
	// Braille cells as the needle so the cell only contains red dots.
	needleDots := [2]int{needleDot, needleDot + 1}
	for row := range dotRows {
		for _, nd := range needleDots {
			if nd >= 0 && nd < dotCols {
				grid[row*dotCols+nd] = cellRed
			}
		}
		// Clear the other dot-column in each Braille cell that contains a
		// needle dot, so the cell doesn't mix red and green.
		for _, nd := range needleDots {
			if nd < 0 || nd >= dotCols {
				continue
			}
			// Braille cells are 2 dot-columns wide. Find the partner column.
			partner := nd ^ 1 // if nd is even, partner is nd+1; if odd, nd-1
			if partner < 0 || partner >= dotCols {
				continue
			}
			// If the partner is not also a needle dot, clear it.
			isNeedle := false
			for _, nd2 := range needleDots {
				if partner == nd2 {
					isNeedle = true
					break
				}
			}
			if !isNeedle && grid[row*dotCols+partner] != cellEmpty {
				grid[row*dotCols+partner] = cellEmpty
			}
		}
	}

	// --- Render grid into Braille characters ---
	greenBright := lipgloss.NewStyle().Foreground(dialGreenLit)
	greenDim := lipgloss.NewStyle().Foreground(dialGreen)
	redStyle := lipgloss.NewStyle().Foreground(dialRed)

	lines := make([]string, height)
	for row := range height {
		var sb strings.Builder
		for col := range width {
			var braille rune = '\u2800'
			maxType := byte(0)
			for dr := range 4 {
				for dc := range 2 {
					gr := row*4 + dr
					gc := col*2 + dc
					if gr < dotRows && gc < dotCols {
						val := grid[gr*dotCols+gc]
						if val > 0 {
							braille |= brailleBit[dr][dc]
						}
						if val > maxType {
							maxType = val
						}
					}
				}
			}

			switch maxType {
			case cellRed:
				sb.WriteString(redStyle.Render(string(braille)))
			case cellGreen:
				sb.WriteString(greenBright.Render(string(braille)))
			case cellGreenDim:
				sb.WriteString(greenDim.Render(string(braille)))
			default:
				sb.WriteRune(' ')
			}
		}
		lines[row] = sb.String()
	}

	// --- Tick labels below the dial in green ---
	labelLine := renderDialLabels(majorTicks, minF, maxF, width, dialGreenLit)

	// --- Frequency readout centered above the dial ---
	readout := formatDialReadout(freq, band)
	readoutStyle := lipgloss.NewStyle().Foreground(dialRed).Bold(true)
	bandStyle := lipgloss.NewStyle().Foreground(dialGreen)

	readoutStr := bandStyle.Render(band+" ") + readoutStyle.Render(readout)
	rawLen := len(band) + 1 + len(readout)
	readoutPad := ""
	if rawLen < width {
		readoutPad = strings.Repeat(" ", (width-rawLen)/2)
	}

	result := []string{readoutPad + readoutStr}
	result = append(result, lines...)
	result = append(result, labelLine)

	return strings.Join(result, "\n")
}

func clampF(v, lo, hi float64) float64 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// formatDialReadout formats the large frequency display.
func formatDialReadout(freq float64, band string) string {
	if band == "AM" {
		return fmt.Sprintf("%.0f kHz", freq)
	}
	return fmt.Sprintf("%.1f MHz", freq)
}

// renderDialLabels renders the tick frequency labels below the dial.
func renderDialLabels(ticks []float64, minF, maxF float64, width int, labelColor lipgloss.ANSIColor) string {
	style := lipgloss.NewStyle().Foreground(labelColor)

	runes := make([]rune, width)
	for i := range runes {
		runes[i] = ' '
	}

	for _, t := range ticks {
		col := int(math.Round(float64(width-1) * (t - minF) / (maxF - minF)))
		label := formatTickLabel(t)
		text := []rune(label)
		start := col - len(text)/2
		if start < 0 {
			start = 0
		}
		if start+len(text) > width {
			start = width - len(text)
		}
		overlap := false
		for j := start; j < start+len(text) && j < width; j++ {
			if runes[j] != ' ' {
				overlap = true
				break
			}
		}
		if !overlap {
			for j, r := range text {
				if start+j < width {
					runes[start+j] = r
				}
			}
		}
	}

	return style.Render(string(runes))
}

// formatTickLabel formats a tick value for the scale.
func formatTickLabel(t float64) string {
	if t >= 200 { // AM
		return fmt.Sprintf("%4.0f", t)
	}
	if t == math.Trunc(t) {
		return fmt.Sprintf("%3.0f", t)
	}
	return fmt.Sprintf("%5.1f", t)
}
