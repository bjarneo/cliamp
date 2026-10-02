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

// Palette holds the colors that a theme gives the UI. A nil Background keeps
// the terminal background.
type Palette struct {
	Background   color.Color
	Title        color.Color
	Text         color.Color
	Dim          color.Color
	Accent       color.Color
	Playing      color.Color
	SeekBar      color.Color
	Volume       color.Color
	Error        color.Color
	Warning      color.Color
	KeyBG        color.Color
	KeyFG        color.Color
	SpectrumLow  color.Color
	SpectrumMid  color.Color
	SpectrumHigh color.Color
}

// PaletteFor returns the colors of t and changes no UI state. The default
// theme, with empty hex values, maps to the standard ANSI terminal colors.
func PaletteFor(t theme.Theme) Palette {
	if t.IsDefault() {
		return Palette{
			Title:        lipgloss.ANSIColor(10),
			Text:         lipgloss.ANSIColor(15),
			Dim:          lipgloss.ANSIColor(7),
			Accent:       lipgloss.ANSIColor(11),
			Playing:      lipgloss.ANSIColor(10),
			SeekBar:      lipgloss.ANSIColor(11),
			Volume:       lipgloss.ANSIColor(2),
			Error:        lipgloss.ANSIColor(9),
			Warning:      lipgloss.ANSIColor(11),
			KeyBG:        lipgloss.ANSIColor(8),
			KeyFG:        lipgloss.ANSIColor(15),
			SpectrumLow:  lipgloss.ANSIColor(10),
			SpectrumMid:  lipgloss.ANSIColor(11),
			SpectrumHigh: lipgloss.ANSIColor(9),
		}
	}
	p := Palette{
		Title:        lipgloss.Color(t.Accent),
		Text:         lipgloss.Color(t.BrightFG),
		Dim:          lipgloss.Color(t.FG),
		Accent:       lipgloss.Color(t.Accent),
		Playing:      lipgloss.Color(t.Green),
		SeekBar:      lipgloss.Color(t.Accent),
		Volume:       lipgloss.Color(t.Green),
		Error:        lipgloss.Color(t.Red),
		Warning:      lipgloss.Color(t.Yellow),
		KeyBG:        lipgloss.Color(t.Accent),
		KeyFG:        lipgloss.Color(contrastingTextColor(t.Accent)),
		SpectrumLow:  lipgloss.Color(t.Green),
		SpectrumMid:  lipgloss.Color(t.Yellow),
		SpectrumHigh: lipgloss.Color(t.Red),
	}
	if t.BG != "" {
		p.Background = lipgloss.Color(t.BG)
	}
	return p
}

// ApplyThemeColors sets the color variables to the palette of t and rebuilds
// the visualizer ANSI that derives from them.
func ApplyThemeColors(t theme.Theme) {
	p := PaletteFor(t)
	ColorBackground = p.Background
	ColorTitle = p.Title
	ColorText = p.Text
	ColorDim = p.Dim
	ColorAccent = p.Accent
	ColorPlaying = p.Playing
	ColorSeekBar = p.SeekBar
	ColorVolume = p.Volume
	ColorError = p.Error
	ColorWarning = p.Warning
	ColorKeyBG = p.KeyBG
	ColorKeyFG = p.KeyFG
	SpectrumLow = p.SpectrumLow
	SpectrumMid = p.SpectrumMid
	SpectrumHigh = p.SpectrumHigh

	refreshSpecANSI()
	refreshRedSectorANSI()
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
