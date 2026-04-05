package model

import (
	"charm.land/lipgloss/v2"

	"cliamp/ui"
)

// Model-specific lipgloss styles, rebuilt when the theme changes.
var (
	titleStyle = lipgloss.NewStyle().
			Foreground(ui.ColorTitle).
			Bold(true)

	trackStyle = lipgloss.NewStyle().
			Foreground(ui.ColorAccent)

	timeStyle = lipgloss.NewStyle().
			Foreground(ui.ColorText)

	statusStyle = lipgloss.NewStyle().
			Foreground(ui.ColorPlaying).
			Bold(true)

	dimStyle = lipgloss.NewStyle().
			Foreground(ui.ColorDim)

	labelStyle = lipgloss.NewStyle().
			Foreground(ui.ColorText).
			Bold(true)

	eqActiveStyle = lipgloss.NewStyle().
			Foreground(ui.ColorAccent).
			Bold(true)

	eqInactiveStyle = lipgloss.NewStyle().
			Foreground(ui.ColorDim)

	playlistActiveStyle = lipgloss.NewStyle().
				Foreground(ui.ColorPlaying).
				Bold(true)

	playlistItemStyle = lipgloss.NewStyle().
				Foreground(ui.ColorText)

	playlistSelectedStyle = lipgloss.NewStyle().
				Foreground(ui.ColorAccent).
				Bold(true)

	helpStyle = lipgloss.NewStyle().
			Foreground(ui.ColorDim)

	helpKeyStyle = lipgloss.NewStyle().
				Foreground(ui.ColorKeyFG).
				Background(ui.ColorKeyBG).
				Bold(true)

	errorStyle = lipgloss.NewStyle().
			Foreground(ui.ColorError)
)

// rebuildModelStyles reconstructs all model-specific lipgloss styles from current color variables.
func rebuildModelStyles() {
	titleStyle = lipgloss.NewStyle().Foreground(ui.ColorTitle).Bold(true)
	trackStyle = lipgloss.NewStyle().Foreground(ui.ColorAccent)
	timeStyle = lipgloss.NewStyle().Foreground(ui.ColorText)
	statusStyle = lipgloss.NewStyle().Foreground(ui.ColorPlaying).Bold(true)
	dimStyle = lipgloss.NewStyle().Foreground(ui.ColorDim)
	labelStyle = lipgloss.NewStyle().Foreground(ui.ColorText).Bold(true)
	eqActiveStyle = lipgloss.NewStyle().Foreground(ui.ColorAccent).Bold(true)
	eqInactiveStyle = lipgloss.NewStyle().Foreground(ui.ColorDim)
	playlistActiveStyle = lipgloss.NewStyle().Foreground(ui.ColorPlaying).Bold(true)
	playlistItemStyle = lipgloss.NewStyle().Foreground(ui.ColorText)
	playlistSelectedStyle = lipgloss.NewStyle().Foreground(ui.ColorAccent).Bold(true)
	helpStyle = lipgloss.NewStyle().Foreground(ui.ColorDim)
	helpKeyStyle = lipgloss.NewStyle().Foreground(ui.ColorKeyFG).Background(ui.ColorKeyBG).Bold(true)
	errorStyle = lipgloss.NewStyle().Foreground(ui.ColorError)

	seekFillStyle = lipgloss.NewStyle().Foreground(ui.ColorSeekBar)
	seekDimStyle = lipgloss.NewStyle().Foreground(ui.ColorDim)
	volBarStyle = lipgloss.NewStyle().Foreground(ui.ColorVolume)
	activeToggle = lipgloss.NewStyle().Foreground(ui.ColorAccent).Bold(true)
}
