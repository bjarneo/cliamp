package ui

import "testing"

// TestVisualizerColumnsComeOnlyFromCols pins that the width comes from Cols.
// A visualizer that nobody sized has no width.
func TestVisualizerColumnsComeOnlyFromCols(t *testing.T) {
	tests := []struct {
		name string
		v    *Visualizer
		want int
	}{
		{name: "sized", v: &Visualizer{Cols: 20}, want: 20},
		{name: "unsized", v: &Visualizer{}},
		{name: "negative", v: &Visualizer{Cols: -1}},
		{name: "nil", v: nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.v.columns(); got != tt.want {
				t.Fatalf("columns() = %d, want %d", got, tt.want)
			}
		})
	}
}
