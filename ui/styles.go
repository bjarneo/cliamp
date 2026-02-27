package ui

import (
	"cliamp/theme"

	"github.com/charmbracelet/lipgloss"
)

// CLIAMP color palette using standard ANSI terminal colors (0-15).
// These adapt to the user's terminal theme for consistent appearance.
var (
	colorTitle   lipgloss.TerminalColor = lipgloss.ANSIColor(10) // bright green
	colorText    lipgloss.TerminalColor = lipgloss.ANSIColor(15) // bright white
	colorDim     lipgloss.TerminalColor = lipgloss.ANSIColor(7)  // white (light gray)
	colorAccent  lipgloss.TerminalColor = lipgloss.ANSIColor(11) // bright yellow
	colorPlaying lipgloss.TerminalColor = lipgloss.ANSIColor(10) // bright green
	colorSeekBar lipgloss.TerminalColor = lipgloss.ANSIColor(11) // bright yellow
	colorVolume  lipgloss.TerminalColor = lipgloss.ANSIColor(2)  // green
	colorError   lipgloss.TerminalColor = lipgloss.ANSIColor(9)  // bright red

	// Spectrum gradient: green -> yellow -> red
	spectrumLow  lipgloss.TerminalColor = lipgloss.ANSIColor(10) // bright green
	spectrumMid  lipgloss.TerminalColor = lipgloss.ANSIColor(11) // bright yellow
	spectrumHigh lipgloss.TerminalColor = lipgloss.ANSIColor(9)  // bright red
)

// panelWidth is the usable inner width (80 frame - 6 padding).
const panelWidth = 74

// Lip Gloss styles
var (
	frameStyle = lipgloss.NewStyle().
			Padding(1, 3).
			Width(80)

	titleStyle = lipgloss.NewStyle().
			Foreground(colorTitle).
			Bold(true)

	trackStyle = lipgloss.NewStyle().
			Foreground(colorAccent)

	timeStyle = lipgloss.NewStyle().
			Foreground(colorText)

	statusStyle = lipgloss.NewStyle().
			Foreground(colorPlaying).
			Bold(true)

	dimStyle = lipgloss.NewStyle().
			Foreground(colorDim)

	labelStyle = lipgloss.NewStyle().
			Foreground(colorText).
			Bold(true)

	eqActiveStyle = lipgloss.NewStyle().
			Foreground(colorAccent).
			Bold(true)

	eqInactiveStyle = lipgloss.NewStyle().
			Foreground(colorDim)

	playlistActiveStyle = lipgloss.NewStyle().
				Foreground(colorPlaying).
				Bold(true)

	playlistItemStyle = lipgloss.NewStyle().
				Foreground(colorText)

	playlistSelectedStyle = lipgloss.NewStyle().
				Foreground(colorAccent).
				Bold(true)

	helpStyle = lipgloss.NewStyle().
			Foreground(colorDim)

	errorStyle = lipgloss.NewStyle().
			Foreground(colorError)
)

// applyTheme updates all color variables and rebuilds derived styles.
// If the theme is the default (empty hex values), ANSI fallback colors are restored.
func applyTheme(t theme.Theme) {
	if t.IsDefault() {
		// Restore ANSI defaults.
		colorTitle = lipgloss.ANSIColor(10)
		colorText = lipgloss.ANSIColor(15)
		colorDim = lipgloss.ANSIColor(7)
		colorAccent = lipgloss.ANSIColor(11)
		colorPlaying = lipgloss.ANSIColor(10)
		colorSeekBar = lipgloss.ANSIColor(11)
		colorVolume = lipgloss.ANSIColor(2)
		colorError = lipgloss.ANSIColor(9)
		spectrumLow = lipgloss.ANSIColor(10)
		spectrumMid = lipgloss.ANSIColor(11)
		spectrumHigh = lipgloss.ANSIColor(9)
	} else {
		colorTitle = lipgloss.Color(t.Accent)
		colorText = lipgloss.Color(t.BrightFG)
		colorDim = lipgloss.Color(t.FG)
		colorAccent = lipgloss.Color(t.Accent)
		colorPlaying = lipgloss.Color(t.Green)
		colorSeekBar = lipgloss.Color(t.Accent)
		colorVolume = lipgloss.Color(t.Green)
		colorError = lipgloss.Color(t.Red)
		spectrumLow = lipgloss.Color(t.Green)
		spectrumMid = lipgloss.Color(t.Yellow)
		spectrumHigh = lipgloss.Color(t.Red)
	}

	rebuildStyles()
}

// rebuildStyles reconstructs all lipgloss styles from current color variables.
func rebuildStyles() {
	// styles.go styles
	titleStyle = lipgloss.NewStyle().Foreground(colorTitle).Bold(true)
	trackStyle = lipgloss.NewStyle().Foreground(colorAccent)
	timeStyle = lipgloss.NewStyle().Foreground(colorText)
	statusStyle = lipgloss.NewStyle().Foreground(colorPlaying).Bold(true)
	dimStyle = lipgloss.NewStyle().Foreground(colorDim)
	labelStyle = lipgloss.NewStyle().Foreground(colorText).Bold(true)
	eqActiveStyle = lipgloss.NewStyle().Foreground(colorAccent).Bold(true)
	eqInactiveStyle = lipgloss.NewStyle().Foreground(colorDim)
	playlistActiveStyle = lipgloss.NewStyle().Foreground(colorPlaying).Bold(true)
	playlistItemStyle = lipgloss.NewStyle().Foreground(colorText)
	playlistSelectedStyle = lipgloss.NewStyle().Foreground(colorAccent).Bold(true)
	helpStyle = lipgloss.NewStyle().Foreground(colorDim)
	errorStyle = lipgloss.NewStyle().Foreground(colorError)

	// view.go pre-built styles
	seekFillStyle = lipgloss.NewStyle().Foreground(colorSeekBar)
	seekDimStyle = lipgloss.NewStyle().Foreground(colorDim)
	volBarStyle = lipgloss.NewStyle().Foreground(colorVolume)
	activeToggle = lipgloss.NewStyle().Foreground(colorAccent).Bold(true)

	// visualizer.go pre-built styles
	specLowStyle = lipgloss.NewStyle().Foreground(spectrumLow)
	specMidStyle = lipgloss.NewStyle().Foreground(spectrumMid)
	specHighStyle = lipgloss.NewStyle().Foreground(spectrumHigh)
}
