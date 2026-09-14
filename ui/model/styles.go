package model

import (
	"charm.land/lipgloss/v2"

	"github.com/bjarneo/cliamp/ui"
)

// Model-specific lipgloss styles. They have no initializers: rebuildModelStyles
// builds them from init for the default palette and again on every theme
// change. A style omitted there renders unstyled under every theme.
var (
	titleStyle               lipgloss.Style
	trackStyle               lipgloss.Style
	timeStyle                lipgloss.Style
	statusStyle              lipgloss.Style
	feedbackActivityStyle    lipgloss.Style
	feedbackSuccessStyle     lipgloss.Style
	feedbackWarningStyle     lipgloss.Style
	dimStyle                 lipgloss.Style
	labelStyle               lipgloss.Style
	eqActiveStyle            lipgloss.Style
	eqInactiveStyle          lipgloss.Style
	playlistActiveStyle      lipgloss.Style
	playlistItemStyle        lipgloss.Style
	playlistSelectedStyle    lipgloss.Style
	playlistUnavailableStyle lipgloss.Style
	helpStyle                lipgloss.Style
	helpKeyStyle             lipgloss.Style
	errorStyle               lipgloss.Style
)

func init() { rebuildModelStyles() }

// rebuildModelStyles reconstructs all model-specific lipgloss styles from current color variables.
func rebuildModelStyles() {
	titleStyle = lipgloss.NewStyle().Foreground(ui.ColorTitle).Bold(true)
	trackStyle = lipgloss.NewStyle().Foreground(ui.ColorAccent)
	timeStyle = lipgloss.NewStyle().Foreground(ui.ColorText)
	statusStyle = lipgloss.NewStyle().Foreground(ui.ColorPlaying).Bold(true)
	feedbackActivityStyle = lipgloss.NewStyle().Foreground(ui.ColorDim)
	feedbackSuccessStyle = lipgloss.NewStyle().Foreground(ui.ColorPlaying).Bold(true)
	feedbackWarningStyle = lipgloss.NewStyle().Foreground(ui.ColorWarning).Bold(true)
	dimStyle = lipgloss.NewStyle().Foreground(ui.ColorDim)
	labelStyle = lipgloss.NewStyle().Foreground(ui.ColorText).Bold(true)
	eqActiveStyle = lipgloss.NewStyle().Foreground(ui.ColorAccent).Bold(true)
	eqInactiveStyle = lipgloss.NewStyle().Foreground(ui.ColorDim)
	playlistActiveStyle = lipgloss.NewStyle().Foreground(ui.ColorPlaying).Bold(true)
	playlistItemStyle = lipgloss.NewStyle().Foreground(ui.ColorText)
	playlistSelectedStyle = lipgloss.NewStyle().Foreground(ui.ColorAccent).Bold(true)
	playlistUnavailableStyle = lipgloss.NewStyle().Foreground(ui.ColorDim)
	helpStyle = lipgloss.NewStyle().Foreground(ui.ColorDim)
	helpKeyStyle = lipgloss.NewStyle().Foreground(ui.ColorKeyFG).Background(ui.ColorKeyBG).Bold(true)
	errorStyle = lipgloss.NewStyle().Foreground(ui.ColorError)

	seekFillStyle = lipgloss.NewStyle().Foreground(ui.ColorSeekBar)
	seekDimStyle = lipgloss.NewStyle().Foreground(ui.ColorDim)
	volBarStyle = lipgloss.NewStyle().Foreground(ui.ColorVolume)
	activeToggle = lipgloss.NewStyle().Foreground(ui.ColorAccent).Bold(true)
	favMarkerStyle = lipgloss.NewStyle().Foreground(ui.ColorError)
	favRemovedStyle = lipgloss.NewStyle().Foreground(ui.ColorDim)
}
