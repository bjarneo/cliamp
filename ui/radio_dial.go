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

// RenderRadioDial draws a retro frequency dial for the given frequency string.
// width is the available character width. Returns empty string if the frequency
// cannot be parsed.
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

	// Clamp frequency to band range for positioning.
	pos := freq
	if pos < minF {
		pos = minF
	}
	if pos > maxF {
		pos = maxF
	}

	// Build the dial scale line.
	scaleWidth := width - 6 // reserve space for band label + borders
	if scaleWidth < 20 {
		scaleWidth = 20
	}

	// Position of needle on the scale (0-based column index).
	needleCol := int(math.Round(float64(scaleWidth-1) * (pos - minF) / (maxF - minF)))

	// --- Line 1: Band label and frequency display ---
	freqDisplay := formatFreqDisplay(freq, band)
	line1 := fmt.Sprintf("  %s  %s", band, freqDisplay)

	// --- Line 2: Tick labels ---
	tickLine := make([]byte, scaleWidth)
	for i := range tickLine {
		tickLine[i] = ' '
	}
	// Place tick labels on the scale.
	type tickLabel struct {
		col  int
		text string
	}
	var labels []tickLabel
	for _, t := range ticks {
		col := int(math.Round(float64(scaleWidth-1) * (t - minF) / (maxF - minF)))
		if col < 0 || col >= scaleWidth {
			continue
		}
		text := formatTickLabel(t)
		labels = append(labels, tickLabel{col: col, text: text})
	}

	// Render tick labels, avoiding overlap.
	labelLine := strings.Repeat(" ", scaleWidth)
	labelRunes := []rune(labelLine)
	for _, lbl := range labels {
		text := []rune(lbl.text)
		start := lbl.col - len(text)/2
		if start < 0 {
			start = 0
		}
		if start+len(text) > scaleWidth {
			start = scaleWidth - len(text)
		}
		// Check for overlap.
		overlap := false
		for j := start; j < start+len(text) && j < scaleWidth; j++ {
			if labelRunes[j] != ' ' {
				overlap = true
				break
			}
		}
		if !overlap {
			for j, r := range text {
				if start+j < scaleWidth {
					labelRunes[start+j] = r
				}
			}
		}
	}

	// --- Line 3: Scale with ticks ---
	scale := make([]rune, scaleWidth)
	for i := range scale {
		scale[i] = '─'
	}
	// Mark tick positions.
	for _, t := range ticks {
		col := int(math.Round(float64(scaleWidth-1) * (t - minF) / (maxF - minF)))
		if col >= 0 && col < scaleWidth {
			scale[col] = '┼'
		}
	}

	// --- Line 4: Needle indicator ---
	needleLine := make([]rune, scaleWidth)
	for i := range needleLine {
		needleLine[i] = ' '
	}
	if needleCol >= 0 && needleCol < scaleWidth {
		needleLine[needleCol] = '▲'
	}

	// Style everything.
	dimStyle := lipgloss.NewStyle().Foreground(ColorDim)
	accentStyle := lipgloss.NewStyle().Foreground(ColorAccent)
	titleStyle := lipgloss.NewStyle().Foreground(ColorTitle).Bold(true)

	// Pad each line with leading spaces for centering the scale portion.
	pad := "  "

	// Color the needle line — highlight just the needle.
	needleStr := colorNeedleLine(needleLine, needleCol, accentStyle, dimStyle)

	// Color the scale — highlight the filled portion up to the needle.
	scaleStr := colorScale(scale, needleCol, accentStyle, dimStyle)

	lines := []string{
		titleStyle.Render(line1),
		dimStyle.Render(pad + string(labelRunes)),
		pad + scaleStr,
		pad + needleStr,
	}

	return strings.Join(lines, "\n")
}

// formatFreqDisplay formats the main frequency readout.
func formatFreqDisplay(freq float64, band string) string {
	if band == "AM" {
		return fmt.Sprintf("[ %4.0f kHz ]", freq)
	}
	return fmt.Sprintf("[ %5.1f MHz ]", freq)
}

// formatTickLabel formats a tick value for the scale.
func formatTickLabel(t float64) string {
	if t >= 200 { // AM
		return fmt.Sprintf("%4.0f", t)
	}
	// FM: show whole numbers without decimal.
	if t == math.Trunc(t) {
		return fmt.Sprintf("%3.0f", t)
	}
	return fmt.Sprintf("%5.1f", t)
}

// colorScale renders the scale runes with accent color up to and including the
// needle position, and dim color for the rest.
func colorScale(scale []rune, needleCol int, accent, dim lipgloss.Style) string {
	if len(scale) == 0 {
		return ""
	}
	var b strings.Builder
	for i, r := range scale {
		if i <= needleCol {
			b.WriteString(accent.Render(string(r)))
		} else {
			b.WriteString(dim.Render(string(r)))
		}
	}
	return b.String()
}

// colorNeedleLine renders the needle indicator line.
func colorNeedleLine(line []rune, needleCol int, accent, dim lipgloss.Style) string {
	var b strings.Builder
	for i, r := range line {
		if i == needleCol {
			b.WriteString(accent.Render(string(r)))
		} else {
			b.WriteString(dim.Render(string(r)))
		}
	}
	return b.String()
}
