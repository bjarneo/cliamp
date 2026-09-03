package ui

import (
	"strings"
	"testing"
)

// TestCharmVisualizersRender exercises each of the recently added visualizers
// (Vinyl through VUMeter). It guards against regressions where the mode isn't
// registered in visNameMap, the renderer panics on combined silence + signal
// transitions, or the output lines don't match v.Rows (which would corrupt
// the panel layout).
func TestCharmVisualizersRender(t *testing.T) {
	cases := []string{
		"Firefly",
		"Mosaic",
		"Sand",
		"ClassicLED",
		"Void",
	}

	signal := make([]float64, 2048)
	for i := range signal {
		signal[i] = float64((i*17)%100)/100.0 - 0.5
	}
	silence := make([]float64, 2048)

	for _, name := range cases {
		t.Run(name, func(t *testing.T) {
			mode, ok := StringToVisModeExact(name)
			if !ok {
				t.Fatalf("mode %q not registered in visNameMap", name)
			}
			v := NewVisualizer(44100)
			v.Rows = 5
			v.Mode = mode

			active := signal
			ctx := VisTickContext{
				Playing: true,
				Analyze: func(spec VisAnalysisSpec) []float64 {
					return v.Analyze(active, spec)
				},
			}

			for i := 0; i < 60; i++ {
				if i%15 == 0 {
					active = silence
				} else {
					active = signal
				}
				v.Tick(ctx)
				out := v.Render()
				if out == "" {
					t.Fatalf("frame %d: empty render", i)
				}
				lines := strings.Split(out, "\n")
				if len(lines) != v.Rows {
					t.Fatalf("frame %d: %d lines, want %d", i, len(lines), v.Rows)
				}
			}
		})
	}
}
