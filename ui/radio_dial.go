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

// dialTicksFM are the major tick marks on the FM dial.
var dialTicksFM = []float64{88, 90, 92, 94, 96, 98, 100, 102, 104, 106, 108}

// dialTicksAM are the major tick marks on the AM dial.
var dialTicksAM = []float64{600, 800, 1000, 1200, 1400, 1600}

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

	// Auto-detect band from frequency range if not explicitly stated.
	if freq > 200 {
		band = "AM"
	}

	return freq, band
}

// dialRows is the height of the LED dot-matrix dial in terminal rows.
const dialRows = 4

// dialColor is a light blue reminiscent of classic Pioneer/Marantz receiver
// tuner dials from the 1970s–80s.
var dialColor = lipgloss.ANSIColor(14) // bright cyan

// RenderRadioDial draws a retro LED dot-matrix frequency dial using Braille
// characters in light blue. The band to the left of the needle is bright, the
// right side is dim. A uniform-width needle line rises from bottom to top.
// Tick marks punctuate at major frequencies.
func RenderRadioDial(freqStr string, width int) string {
	freq, band := ParseFrequency(freqStr)
	if freq == 0 {
		return ""
	}

	if width < 30 {
		width = 30
	}

	var minF, maxF float64
	var ticks []float64
	if band == "AM" {
		minF, maxF, ticks = amMin, amMax, dialTicksAM
	} else {
		minF, maxF, ticks = fmMin, fmMax, dialTicksFM
	}

	pos := clampF(freq, minF, maxF)

	dotCols := width * 2
	height := dialRows
	dotRows := height * 4

	// Needle position in dot-column space.
	needleDot := int(math.Round(float64(dotCols-1) * (pos - minF) / (maxF - minF)))

	// Grid stores brightness: 0=empty, 1=dim, 2=bright.
	grid := make([]byte, dotRows*dotCols)

	// --- Filled band: bottom 40% is a solid lit strip ---
	// Left of needle = bright (2), right of needle = dim (1).
	bandHeight := dotRows * 2 / 5
	if bandHeight < 4 {
		bandHeight = 4
	}
	for dr := 0; dr < bandHeight; dr++ {
		row := dotRows - 1 - dr
		for dc := range dotCols {
			if dc <= needleDot {
				grid[row*dotCols+dc] = 2
			} else {
				grid[row*dotCols+dc] = 1
			}
		}
	}

	// --- Tick marks: short columns rising above the band ---
	tickRise := 5 // dots above the band top
	for _, t := range ticks {
		tc := int(math.Round(float64(dotCols-1) * (t - minF) / (maxF - minF)))
		if tc < 0 || tc >= dotCols {
			continue
		}
		topOfBand := dotRows - bandHeight
		for dr := 0; dr < bandHeight+tickRise && dotRows-1-dr >= 0; dr++ {
			row := dotRows - 1 - dr
			brightness := byte(1)
			if tc <= needleDot {
				brightness = 2
			}
			if row >= topOfBand {
				brightness = max(brightness, grid[row*dotCols+tc])
			}
			if grid[row*dotCols+tc] < brightness {
				grid[row*dotCols+tc] = brightness
			}
		}
	}

	// --- Needle: uniform 3-dot-wide line from bottom to top ---
	for row := range dotRows {
		// Center column: bright.
		if needleDot >= 0 && needleDot < dotCols {
			grid[row*dotCols+needleDot] = 2
		}
		// One dot on each side: bright.
		if c := needleDot - 1; c >= 0 && c < dotCols {
			if grid[row*dotCols+c] < 2 {
				grid[row*dotCols+c] = 2
			}
		}
		if c := needleDot + 1; c >= 0 && c < dotCols {
			if grid[row*dotCols+c] < 2 {
				grid[row*dotCols+c] = 2
			}
		}
	}

	// --- Render grid into Braille characters, all in light blue ---
	dialBright := lipgloss.NewStyle().Foreground(dialColor)
	dialDim := lipgloss.NewStyle().Foreground(dialColor).Faint(true)

	lines := make([]string, height)
	for row := range height {
		var sb strings.Builder
		for col := range width {
			var braille rune = '\u2800'
			maxBright := byte(0)
			for dr := range 4 {
				for dc := range 2 {
					gr := row*4 + dr
					gc := col*2 + dc
					if gr < dotRows && gc < dotCols {
						val := grid[gr*dotCols+gc]
						if val > 0 {
							braille |= brailleBit[dr][dc]
						}
						if val > maxBright {
							maxBright = val
						}
					}
				}
			}

			switch maxBright {
			case 2:
				sb.WriteString(dialBright.Render(string(braille)))
			case 1:
				sb.WriteString(dialDim.Render(string(braille)))
			default:
				sb.WriteRune(' ')
			}
		}
		lines[row] = sb.String()
	}

	// --- Tick labels below the dial ---
	labelLine := renderDialLabels(ticks, minF, maxF, width)

	// --- Frequency readout centered above the dial ---
	readout := formatDialReadout(freq, band)
	readoutStyle := lipgloss.NewStyle().Foreground(dialColor).Bold(true)
	bandStyle := lipgloss.NewStyle().Foreground(ColorDim)

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
func renderDialLabels(ticks []float64, minF, maxF float64, width int) string {
	dimStyle := lipgloss.NewStyle().Foreground(ColorDim)

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

	return dimStyle.Render(string(runes))
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
