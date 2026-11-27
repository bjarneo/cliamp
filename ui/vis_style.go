package ui

import (
	"image/color"
	"strings"

	"charm.land/lipgloss/v2"
)

// Unicode block elements for bar height (9 levels including space)
var barBlocks = []string{" ", "▁", "▂", "▃", "▄", "▅", "▆", "▇", "█"}

// brailleUTF8 stores the UTF-8 string for each 8-dot Braille mask.
var brailleUTF8 [256]string

func init() {
	for m := range brailleUTF8 {
		brailleUTF8[m] = string(rune('\u2800' | rune(m)))
	}
}

// styleANSI is the raw ANSI that a style writes before and after its text.
// Caching it once lets every style-run flush skip lipgloss.Render, which
// allocates a fresh wrapped string per call, and instead stream prefix, body
// and suffix into an existing builder.
type styleANSI struct{ prefix, suffix string }

// foregroundANSI renders a rare marker through a plain foreground style in c
// and splits the output around it. Borders or padding would invalidate the
// split, so the style carries the colour only.
func foregroundANSI(c color.Color) styleANSI {
	return colorANSI(lipgloss.NewStyle().Foreground(c))
}

// pairStyle colours fg over bg. A NoColor background stands for the
// terminal's default foreground, which has no SGR of its own, so the pair is
// drawn in reverse video: the terminal paints its foreground behind the cell
// and fg on top.
func pairStyle(fg, bg color.Color) lipgloss.Style {
	if _, ok := bg.(lipgloss.NoColor); ok {
		return lipgloss.NewStyle().Background(fg).Reverse(true)
	}
	return lipgloss.NewStyle().Foreground(fg).Background(bg)
}

// colorANSI splits the output of a colour-only style around a rare marker.
func colorANSI(style lipgloss.Style) styleANSI {
	const probe = "\uFFFC"
	rendered := style.Render(probe)
	idx := strings.Index(rendered, probe)
	if idx < 0 {
		return styleANSI{}
	}
	return styleANSI{prefix: rendered[:idx], suffix: rendered[idx+len(probe):]}
}

// specANSI holds the ANSI of the low, mid and high spectrum colours, indexed
// by specTag. ApplyThemeColors rebuilds it through refreshSpecANSI.
var specANSI [3]styleANSI

func refreshSpecANSI() {
	for i, c := range [3]color.Color{SpectrumLow, SpectrumMid, SpectrumHigh} {
		specANSI[i] = foregroundANSI(c)
	}
}

// fracBlock returns the fractional Unicode block character for a band level
// within the row span [rowBottom, rowTop]. Used by bars and columns visualizers.
func fracBlock(level, rowBottom, rowTop float64) string {
	if level >= rowTop {
		return "█"
	}
	if level > rowBottom {
		frac := (level - rowBottom) / (rowTop - rowBottom)
		idx := int(frac * float64(len(barBlocks)-1))
		idx = max(0, min(idx, len(barBlocks)-1))
		return barBlocks[idx]
	}
	return " "
}

// specTag returns 0, 1, or 2 identifying the spectrum color tier for style-run
// batching, using the same thresholds as specWrap.
func specTag(norm float64) int {
	if norm >= 0.6 {
		return 2
	}
	if norm >= 0.3 {
		return 1
	}
	return 0
}

// specStyle returns the cached ANSI sequence for tag, or an empty style when
// tag is outside the spectrum tiers.
func specStyle(tag int) styleANSI {
	if tag >= 0 && tag < len(specANSI) {
		return specANSI[tag]
	}
	return styleANSI{}
}

// specWrap wraps body in the cached ANSI sequences for the spectrum color at
// the given row-bottom (0-1). One string concatenation instead of the several
// allocations a per-call lipgloss.Style.Render would perform.
func specWrap(rowBottom float64, body string) string {
	style := specANSI[specTag(rowBottom)]
	if style.prefix == "" {
		return body
	}
	return style.prefix + body + style.suffix
}

// styleRunWriter streams styled runs directly into a strings.Builder.
// It emits ANSI prefix and suffix transitions inline without a run buffer.
type styleRunWriter struct {
	sb     *strings.Builder
	active styleANSI
	open   bool
}

func (w *styleRunWriter) switchStyle(style styleANSI) {
	if w.open {
		w.sb.WriteString(w.active.suffix)
		w.open = false
	}
	w.active = style
}

func (w *styleRunWriter) setTag(tag int) {
	w.switchStyle(specStyle(tag))
}

func (w *styleRunWriter) ensureOpen() {
	if !w.open {
		w.sb.WriteString(w.active.prefix)
		w.open = true
	}
}

func (w *styleRunWriter) writeByte(b byte) {
	w.ensureOpen()
	w.sb.WriteByte(b)
}

func (w *styleRunWriter) writeRune(r rune) {
	w.ensureOpen()
	w.sb.WriteRune(r)
}

func (w *styleRunWriter) writeBraille(mask byte) {
	w.ensureOpen()
	w.sb.WriteString(brailleUTF8[mask])
}

func (w *styleRunWriter) writeString(s string) {
	if len(s) == 0 {
		return
	}
	w.ensureOpen()
	w.sb.WriteString(s)
}

func (w *styleRunWriter) flush() {
	if w.open {
		w.sb.WriteString(w.active.suffix)
		w.open = false
	}
}
