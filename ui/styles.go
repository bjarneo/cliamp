package ui

import (
	"cliamp/theme"

	"github.com/charmbracelet/lipgloss"
)

// CLIAMP color palette using standard ANSI terminal colors (0-15).
// These adapt to the user's terminal theme for consistent appearance.
var (
	ColorTitle   lipgloss.TerminalColor = lipgloss.ANSIColor(10) // bright green
	ColorText    lipgloss.TerminalColor = lipgloss.ANSIColor(15) // bright white
	ColorDim     lipgloss.TerminalColor = lipgloss.ANSIColor(7)  // white (light gray)
	ColorAccent  lipgloss.TerminalColor = lipgloss.ANSIColor(11) // bright yellow
	ColorPlaying lipgloss.TerminalColor = lipgloss.ANSIColor(10) // bright green
	ColorSeekBar lipgloss.TerminalColor = lipgloss.ANSIColor(11) // bright yellow
	ColorVolume  lipgloss.TerminalColor = lipgloss.ANSIColor(2)  // green
	ColorError   lipgloss.TerminalColor = lipgloss.ANSIColor(9)  // bright red

	// Spectrum gradient: green -> yellow -> red
	SpectrumLow  lipgloss.TerminalColor = lipgloss.ANSIColor(10) // bright green
	SpectrumMid  lipgloss.TerminalColor = lipgloss.ANSIColor(11) // bright yellow
	SpectrumHigh lipgloss.TerminalColor = lipgloss.ANSIColor(9)  // bright red
)

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

// FrameStyle is the outer frame style for the TUI.
var FrameStyle = lipgloss.NewStyle().
	Padding(paddingV, PaddingH).
	Width(80)

// ApplyThemeColors updates all color variables and rebuilds spectrum styles.
// If the theme is the default (empty hex values), ANSI fallback colors are restored.
func ApplyThemeColors(t theme.Theme) {
	if t.IsDefault() {
		// Restore ANSI defaults.
		ColorTitle = lipgloss.ANSIColor(10)
		ColorText = lipgloss.ANSIColor(15)
		ColorDim = lipgloss.ANSIColor(7)
		ColorAccent = lipgloss.ANSIColor(11)
		ColorPlaying = lipgloss.ANSIColor(10)
		ColorSeekBar = lipgloss.ANSIColor(11)
		ColorVolume = lipgloss.ANSIColor(2)
		ColorError = lipgloss.ANSIColor(9)
		SpectrumLow = lipgloss.ANSIColor(10)
		SpectrumMid = lipgloss.ANSIColor(11)
		SpectrumHigh = lipgloss.ANSIColor(9)
	} else {
		ColorTitle = lipgloss.Color(t.Accent)
		ColorText = lipgloss.Color(t.BrightFG)
		ColorDim = lipgloss.Color(t.FG)
		ColorAccent = lipgloss.Color(t.Accent)
		ColorPlaying = lipgloss.Color(t.Green)
		ColorSeekBar = lipgloss.Color(t.Accent)
		ColorVolume = lipgloss.Color(t.Green)
		ColorError = lipgloss.Color(t.Red)
		SpectrumLow = lipgloss.Color(t.Green)
		SpectrumMid = lipgloss.Color(t.Yellow)
		SpectrumHigh = lipgloss.Color(t.Red)
	}

	// Rebuild visualizer spectrum styles.
	specLowStyle = lipgloss.NewStyle().Foreground(SpectrumLow)
	specMidStyle = lipgloss.NewStyle().Foreground(SpectrumMid)
	specHighStyle = lipgloss.NewStyle().Foreground(SpectrumHigh)
}
