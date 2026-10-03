package ui

import (
	"fmt"
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
)

// The band renderers must fill the panel exactly and leave no band for the
// frame fitter to clip, whatever the width.
func TestBandRenderersFillPanelExactly(t *testing.T) {
	renderers := []struct {
		name   string
		render func(*Visualizer, []float64) string
	}{
		{"Bars", (*Visualizer).renderBars},
		{"BarsDot", (*Visualizer).renderBarsDot},
		{"Rain", (*Visualizer).renderRain},
		{"BarsOutline", (*Visualizer).renderBarsOutline},
		{"Bricks", (*Visualizer).renderBricks},
		{"Columns", (*Visualizer).renderColumns},
		{"Scatter", (*Visualizer).renderScatter},
		{"Matrix", (*Visualizer).renderMatrix},
		{"Binary", (*Visualizer).renderBinary},
	}
	for _, width := range []int{1, 8, 12, 13, 18, 19, 40, 76} {
		for _, r := range renderers {
			t.Run(fmt.Sprintf("%s/%d", r.name, width), func(t *testing.T) {
				v := NewVisualizer(44100)
				v.Cols = width
				v.Rows = 3
				for i, line := range strings.Split(r.render(v, uniformBands(1)), "\n") {
					if got := lipgloss.Width(line); got != width {
						t.Fatalf("row %d width = %d, want %d: %q", i, got, width, line)
					}
				}
			})
		}
	}
}

func TestBandLayoutFillsPanel(t *testing.T) {
	tests := []struct {
		width, bands int
		wantGaps     int
	}{
		{width: 1, bands: 10, wantGaps: 0},
		{width: 8, bands: 10, wantGaps: 0},
		{width: 10, bands: 10, wantGaps: 0},
		{width: 13, bands: 10, wantGaps: 3},
		{width: 18, bands: 10, wantGaps: 8},
		{width: 19, bands: 10, wantGaps: 9},
		{width: 76, bands: 10, wantGaps: 9},
		{width: 76, bands: 64, wantGaps: 12},
	}
	for _, tt := range tests {
		t.Run(fmt.Sprintf("%dx%d", tt.width, tt.bands), func(t *testing.T) {
			cols, gaps := 0, 0
			for b := range tt.bands {
				cols += visBandWidth(tt.bands, b, tt.width)
				if bandGapAfter(tt.bands, b, tt.width) {
					gaps++
					if visBandWidth(tt.bands, b, tt.width) == 0 {
						t.Errorf("gap after band %d, which has no columns", b)
					}
				}
			}
			if gaps != tt.wantGaps {
				t.Errorf("gaps = %d, want %d", gaps, tt.wantGaps)
			}
			if cols+gaps != tt.width {
				t.Errorf("columns %d + gaps %d = %d, want %d", cols, gaps, cols+gaps, tt.width)
			}
		})
	}
}
