package ui

import (
	"strings"

	"github.com/charmbracelet/x/ansi"
)

func fitVisualizerFrame(frame string, cols, rows int) string {
	if cols <= 0 || rows <= 0 {
		return ""
	}

	var out strings.Builder
	out.Grow(rows*(cols+1) - 1)
	rest := frame
	for row := range rows {
		if row > 0 {
			out.WriteByte('\n')
		}
		line, next, _ := strings.Cut(rest, "\n")
		width := writeVisualizerLine(&out, line, cols)
		for range cols - width {
			out.WriteByte(' ')
		}
		rest = next
	}
	return out.String()
}

func writeVisualizerLine(out *strings.Builder, line string, cols int) int {
	width := 0
	clipped := false
	var state byte
	for len(line) > 0 {
		seq, seqWidth, n, nextState := ansi.DecodeSequence(line, state, nil)
		if n == 0 {
			seq, n, nextState = line[:1], 1, ansi.NormalState
		}
		state = nextState
		line = line[n:]

		if !clipped && width+seqWidth <= cols {
			out.WriteString(seq)
			width += seqWidth
			continue
		}
		clipped = true
		if len(seq) > 0 && (seq[0] == ansi.ESC || seq[0] >= ansi.PAD && seq[0] <= ansi.APC) {
			out.WriteString(seq)
		}
	}
	return width
}
