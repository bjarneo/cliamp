package ui

import (
	"image/color"
	"strings"
	"testing"

	"charm.land/lipgloss/v2"

	"github.com/bjarneo/cliamp/theme"
)

func TestContrastingTextColor(t *testing.T) {
	tests := []struct {
		name   string
		accent string
		want   string
	}{
		{name: "light accent", accent: "#f7df50", want: "#000000"},
		{name: "dark accent", accent: "#3e4a5e", want: "#ffffff"},
		{name: "invalid accent", accent: "blue", want: "#ffffff"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := contrastingTextColor(tt.accent); got != tt.want {
				t.Errorf("contrastingTextColor(%q) = %q, want %q", tt.accent, got, tt.want)
			}
		})
	}
}

// testTheme is a complete custom theme with a light accent.
var testTheme = theme.Theme{
	Name:     "test",
	BG:       "#101418",
	Accent:   "#f7df50",
	BrightFG: "#f0f0f0",
	FG:       "#a0a0a0",
	Green:    "#50fa7b",
	Yellow:   "#f1fa8c",
	Red:      "#ff5555",
}

func TestPaletteFor(t *testing.T) {
	noBG := testTheme
	noBG.BG = ""
	tests := []struct {
		name  string
		theme theme.Theme
		want  Palette
	}{
		{
			name:  "default theme uses terminal colors",
			theme: theme.Default(),
			want: Palette{
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
			},
		},
		{
			name:  "custom theme maps its hex colors",
			theme: testTheme,
			want: Palette{
				Background:   lipgloss.Color("#101418"),
				Title:        lipgloss.Color("#f7df50"),
				Text:         lipgloss.Color("#f0f0f0"),
				Dim:          lipgloss.Color("#a0a0a0"),
				Accent:       lipgloss.Color("#f7df50"),
				Playing:      lipgloss.Color("#50fa7b"),
				SeekBar:      lipgloss.Color("#f7df50"),
				Volume:       lipgloss.Color("#50fa7b"),
				Error:        lipgloss.Color("#ff5555"),
				Warning:      lipgloss.Color("#f1fa8c"),
				KeyBG:        lipgloss.Color("#f7df50"),
				KeyFG:        lipgloss.Color("#000000"),
				SpectrumLow:  lipgloss.Color("#50fa7b"),
				SpectrumMid:  lipgloss.Color("#f1fa8c"),
				SpectrumHigh: lipgloss.Color("#ff5555"),
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := PaletteFor(tt.theme); got != tt.want {
				t.Fatalf("PaletteFor() =\n%+v\nwant\n%+v", got, tt.want)
			}
		})
	}
	t.Run("custom theme without a background keeps the terminal background", func(t *testing.T) {
		if got := PaletteFor(noBG).Background; got != nil {
			t.Fatalf("Background = %v, want nil", got)
		}
	})
}

// ApplyThemeColors writes the palette of the theme and the spectrum ANSI that
// the visualizers wrap their runs in.
func TestApplyThemeColors(t *testing.T) {
	t.Cleanup(func() { ApplyThemeColors(theme.Default()) })
	for _, th := range []theme.Theme{theme.Default(), testTheme} {
		t.Run(th.Name, func(t *testing.T) {
			ApplyThemeColors(th)
			p := PaletteFor(th)
			got := Palette{
				Background: ColorBackground, Title: ColorTitle, Text: ColorText,
				Dim: ColorDim, Accent: ColorAccent, Playing: ColorPlaying,
				SeekBar: ColorSeekBar, Volume: ColorVolume, Error: ColorError,
				Warning: ColorWarning, KeyBG: ColorKeyBG, KeyFG: ColorKeyFG,
				SpectrumLow: SpectrumLow, SpectrumMid: SpectrumMid, SpectrumHigh: SpectrumHigh,
			}
			if got != p {
				t.Fatalf("globals =\n%+v\nwant\n%+v", got, p)
			}

			fg := func(c color.Color) string { return lipgloss.NewStyle().Foreground(c).Render("x") }
			for _, tier := range []struct {
				tag   int
				level float64
				want  string
			}{
				{tag: -1, level: -1, want: "x"},
				{tag: 0, level: 0.1, want: fg(p.SpectrumLow)},
				{tag: 1, level: 0.4, want: fg(p.SpectrumMid)},
				{tag: 2, level: 0.9, want: fg(p.SpectrumHigh)},
				{tag: 3, level: -1, want: "x"},
			} {
				var sb, run strings.Builder
				run.WriteString("x")
				flushStyleRun(&sb, &run, tier.tag)
				if sb.String() != tier.want {
					t.Errorf("flushStyleRun(tag %d) = %q, want %q", tier.tag, sb.String(), tier.want)
				}
				if tier.level >= 0 {
					if got := specWrap(tier.level, "x"); got != tier.want {
						t.Errorf("specWrap(%v) = %q, want %q", tier.level, got, tier.want)
					}
				}
			}
			// Red Sector draws its stars in the dim colour and its bars in
			// the spectrum colours.
			for tag, c := range map[int]color.Color{
				1:                p.Dim,
				redSectorTagLow:  p.SpectrumLow,
				redSectorTagMid:  p.SpectrumMid,
				redSectorTagHigh: p.SpectrumHigh,
			} {
				var sb, run strings.Builder
				run.WriteString("x")
				flushRedSectorRun(&sb, &run, tag)
				if want := fg(c); sb.String() != want {
					t.Errorf("flushRedSectorRun(tag %d) = %q, want %q", tag, sb.String(), want)
				}
			}
		})
	}
}
