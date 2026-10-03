package ui

import (
	"image/color"
	"strings"

	"charm.land/lipgloss/v2"
)

// Unicode block elements for bar height (9 levels including space)
var barBlocks = []string{" ", "▁", "▂", "▃", "▄", "▅", "▆", "▇", "█"}

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

// flushStyleRun appends the accumulated run bytes to sb wrapped in the cached
// ANSI sequences for the given tag, then resets run. Tag -1 writes unstyled.
// Streaming via the pre-extracted prefix/suffix strings avoids allocating a
// fresh lipgloss.Render result on every flush (the hot path for Matrix/Pulse).
func flushStyleRun(sb *strings.Builder, run *strings.Builder, tag int) {
	var style styleANSI
	if tag >= 0 && tag < len(specANSI) {
		style = specANSI[tag]
	}
	writeStyledRun(sb, run, style)
}

// writeStyledRun appends run to sb between the ANSI prefix and suffix of its
// style, then resets run. An empty run writes nothing.
func writeStyledRun(sb, run *strings.Builder, style styleANSI) {
	if run.Len() == 0 {
		return
	}
	sb.WriteString(style.prefix)
	// run.String() aliases the builder's backing array (no allocation) and we
	// copy those bytes into sb before run.Reset() releases the slice.
	sb.WriteString(run.String())
	sb.WriteString(style.suffix)
	run.Reset()
}
