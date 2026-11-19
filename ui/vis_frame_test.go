package ui

import (
	"strings"
	"testing"
	"time"
)

// Copying a line that fits gives the same bytes as walking it.
func TestFitVisualizerFrameMatchesWalk(t *testing.T) {
	walked := func(frame string, cols, rows int) string {
		var out strings.Builder
		rest := frame
		for row := range rows {
			if row > 0 {
				out.WriteByte('\n')
			}
			line, next, _ := strings.Cut(rest, "\n")
			out.WriteString(strings.Repeat(" ", cols-writeVisualizerLine(&out, line, cols)))
			rest = next
		}
		return out.String()
	}
	var frames []string
	samples := benchSamplesSine(defaultFFTSize)
	for mode := VisMode(0); mode < VisCount; mode++ {
		v := NewVisualizer(benchSampleRate)
		v.Mode, v.Cols, v.Rows = mode, 201, 20
		ctx := VisTickContext{Now: time.Now(), Playing: true, Analyze: func(spec VisAnalysisSpec) []float64 { return v.Analyze(samples, spec) }}
		for range 4 {
			v.Tick(ctx)
		}
		if d := v.syncDriverMode(); d != nil {
			frames = append(frames, d.Render(v))
		}
	}
	frames = append(frames,
		"\x1b[38;2;255;128;0m"+strings.Repeat("界 visualizer ", 12)+"\x1b[0m",
		"emoji \u2764\ufe0f and e\u0301",
		"\x1b]8;;https://example.com\x1b\\link\x1b]8;;\x1b\\",
	)
	for i, f := range frames {
		for _, cols := range []int{201, 76, 20, 1} {
			if got, want := fitVisualizerFrame(f, cols, 20), walked(f, cols, 20); got != want {
				t.Errorf("frame %d at %d columns: differs", i, cols)
			}
		}
	}
}
