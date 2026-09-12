package ui

import (
	"image/color"
	"math"
	"strconv"
	"strings"

	"charm.land/lipgloss/v2"

	"github.com/bjarneo/cliamp/theme"
)

// CLIAMP color palette. With no theme configured these are the standard ANSI
// terminal colors (0-15), which adapt to the user's terminal theme. They have
// no initializers: ApplyThemeColors is the single place that sets them, from
// init for the default palette and again on every theme change.
var (
	ColorBackground color.Color
	ColorTitle      color.Color
	ColorText       color.Color
	ColorDim        color.Color
	ColorAccent     color.Color
	ColorPlaying    color.Color
	ColorSeekBar    color.Color
	ColorVolume     color.Color
	ColorError      color.Color
	ColorWarning    color.Color
	ColorKeyBG      color.Color
	ColorKeyFG      color.Color
	SpectrumLow     color.Color
	SpectrumMid     color.Color
	SpectrumHigh    color.Color
)

func init() { ApplyThemeColors(theme.Default()) }

// PaddingH is the horizontal padding inside the frame.
var PaddingH = 3

// paddingV is the vertical padding inside the frame.
var paddingV = 1

// PanelWidth is the usable inner width of the frame.
// Updated dynamically in WindowSizeMsg based on terminal width.
var PanelWidth = 80 - 2*PaddingH

// SetPadding updates the frame padding and derived styles.
func SetPadding(h, v int) {
	PaddingH = h
	paddingV = v
	PanelWidth = 80 - 2*PaddingH
	FrameStyle = FrameStyle.Padding(paddingV, PaddingH)
}

// WithPanelWidth narrows PanelWidth for a scope and returns the restore func,
// so callers rendering into a sub-width column can `defer WithPanelWidth(w)()`
// and be sure the frame width comes back even if the call panics.
func WithPanelWidth(w int) func() {
	previous := PanelWidth
	PanelWidth = w
	return func() { PanelWidth = previous }
}

// VerticalPadding returns the current frame padding above and below content.
func VerticalPadding() int {
	return paddingV
}

// FrameStyle is the outer frame style for the TUI.
var FrameStyle = lipgloss.NewStyle().
	Padding(paddingV, PaddingH).
	Width(80)

// ApplyThemeColors updates all color variables and rebuilds spectrum styles.
// If the theme is the default (empty hex values), ANSI fallback colors are restored.
func ApplyThemeColors(t theme.Theme) {
	if t.IsDefault() {
		ColorBackground = nil
		ColorTitle = lipgloss.ANSIColor(10)
		ColorText = lipgloss.ANSIColor(15)
		ColorDim = lipgloss.ANSIColor(7)
		ColorAccent = lipgloss.ANSIColor(11)
		ColorPlaying = lipgloss.ANSIColor(10)
		ColorSeekBar = lipgloss.ANSIColor(11)
		ColorVolume = lipgloss.ANSIColor(2)
		ColorError = lipgloss.ANSIColor(9)
		ColorWarning = lipgloss.ANSIColor(11)
		ColorKeyBG = lipgloss.ANSIColor(8)
		ColorKeyFG = lipgloss.ANSIColor(15)
		SpectrumLow = lipgloss.ANSIColor(10)
		SpectrumMid = lipgloss.ANSIColor(11)
		SpectrumHigh = lipgloss.ANSIColor(9)
	} else {
		if t.BG == "" {
			ColorBackground = nil
		} else {
			ColorBackground = lipgloss.Color(t.BG)
		}
		ColorTitle = lipgloss.Color(t.Accent)
		ColorText = lipgloss.Color(t.BrightFG)
		ColorDim = lipgloss.Color(t.FG)
		ColorAccent = lipgloss.Color(t.Accent)
		ColorPlaying = lipgloss.Color(t.Green)
		ColorSeekBar = lipgloss.Color(t.Accent)
		ColorVolume = lipgloss.Color(t.Green)
		ColorError = lipgloss.Color(t.Red)
		ColorWarning = lipgloss.Color(t.Yellow)
		ColorKeyBG = lipgloss.Color(t.Accent)
		ColorKeyFG = lipgloss.Color(contrastingTextColor(t.Accent))
		SpectrumLow = lipgloss.Color(t.Green)
		SpectrumMid = lipgloss.Color(t.Yellow)
		SpectrumHigh = lipgloss.Color(t.Red)
	}

	// Rebuild visualizer spectrum styles.
	specLowStyle = lipgloss.NewStyle().Foreground(SpectrumLow)
	specMidStyle = lipgloss.NewStyle().Foreground(SpectrumMid)
	specHighStyle = lipgloss.NewStyle().Foreground(SpectrumHigh)
	refreshSpecANSI()
}

func contrastingTextColor(hex string) string {
	value, err := strconv.ParseUint(strings.TrimPrefix(hex, "#"), 16, 24)
	if err != nil {
		return "#ffffff"
	}
	linear := func(channel uint64) float64 {
		component := float64(channel) / 255
		if component <= 0.04045 {
			return component / 12.92
		}
		return math.Pow((component+0.055)/1.055, 2.4)
	}
	luminance := 0.2126*linear(value>>16) + 0.7152*linear((value>>8)&0xff) + 0.0722*linear(value&0xff)
	// This is the crossover where black provides more contrast than white.
	if luminance > 0.179 {
		return "#000000"
	}
	return "#ffffff"
}
