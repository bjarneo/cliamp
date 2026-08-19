package ui

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
)

func TestVisualizerFitsTinyRectangles(t *testing.T) {
	for mode := VisMode(0); mode < VisCount; mode++ {
		t.Run(visModes[mode].name, func(t *testing.T) {
			for _, rect := range []struct{ cols, rows int }{{0, 0}, {1, 1}, {8, 2}} {
				v := NewVisualizer(44100)
				v.Mode = mode
				v.Cols = rect.cols
				v.Rows = rect.rows
				v.Tick(VisTickContext{})
				got := v.Render()
				if rect.cols == 0 || rect.rows == 0 || mode == VisNone {
					if got != "" {
						t.Fatalf("%dx%d render = %q, want empty", rect.cols, rect.rows, got)
					}
					continue
				}
				lines := strings.Split(got, "\n")
				if len(lines) != rect.rows {
					t.Fatalf("%dx%d line count = %d, want %d: %q", rect.cols, rect.rows, len(lines), rect.rows, got)
				}
				for _, line := range lines {
					if width := lipgloss.Width(line); width != rect.cols {
						t.Fatalf("%dx%d line width = %d, want %d: %q", rect.cols, rect.rows, width, rect.cols, line)
					}
				}
			}
		})
	}
}

func TestVisualizerFrameClipsPluginOutput(t *testing.T) {
	frame := fitVisualizerFrame(strings.Repeat("界", 20)+"\n"+strings.Repeat("plugin output ", 20), 8, 2)
	lines := strings.Split(frame, "\n")
	if len(lines) != 2 {
		t.Fatalf("line count = %d, want 2", len(lines))
	}
	for _, line := range lines {
		if width := lipgloss.Width(line); width != 8 {
			t.Fatalf("line width = %d, want 8: %q", width, line)
		}
	}
}

func TestFitVisualizerFrame(t *testing.T) {
	tests := []struct {
		name       string
		frame      string
		cols, rows int
		want       string
	}{
		{name: "empty", frame: "", cols: 3, rows: 2, want: "   \n   "},
		{name: "pads rows", frame: "ab\nc", cols: 4, rows: 3, want: "ab  \nc   \n    "},
		{name: "clips rows and columns", frame: "abcdef\nignored", cols: 3, rows: 1, want: "abc"},
		{name: "wide characters", frame: "a界bc", cols: 3, rows: 1, want: "a界"},
		{name: "wide overflow pads remaining cell", frame: "a界b", cols: 2, rows: 1, want: "a "},
		{name: "combining grapheme", frame: "e\u0301x", cols: 1, rows: 1, want: "e\u0301"},
		{name: "emoji grapheme", frame: "👩‍💻x", cols: 2, rows: 1, want: "👩‍💻"},
		{name: "clipped ANSI style", frame: "\x1b[31mabcdef\x1b[0m", cols: 3, rows: 1, want: "\x1b[31mabc\x1b[0m"},
		{name: "ANSI reset after wide overflow", frame: "\x1b[31ma界b\x1b[0m", cols: 2, rows: 1, want: "\x1b[31ma\x1b[0m "},
		{name: "padded ANSI style", frame: "\x1b[31ma\x1b[0m", cols: 3, rows: 1, want: "\x1b[31ma\x1b[0m  "},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := fitVisualizerFrame(tt.frame, tt.cols, tt.rows)
			if got != tt.want {
				t.Fatalf("fitVisualizerFrame(%q, %d, %d) = %q, want %q", tt.frame, tt.cols, tt.rows, got, tt.want)
			}
			lines := strings.Split(got, "\n")
			if len(lines) != tt.rows {
				t.Fatalf("line count = %d, want %d", len(lines), tt.rows)
			}
			for _, line := range lines {
				if width := lipgloss.Width(line); width != tt.cols {
					t.Fatalf("line width = %d, want %d: %q", width, tt.cols, line)
				}
			}
		})
	}
}
