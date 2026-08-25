package ui

import (
	"testing"

	"github.com/charmbracelet/x/ansi"
)

func TestMirrorMode(t *testing.T) {
	mode, ok := StringToVisModeExact("Mirror")
	if !ok || mode != VisMirror {
		t.Fatalf("StringToVisModeExact(Mirror) = (%v, %v), want (%v, true)", mode, ok, VisMirror)
	}

	v := NewVisualizer(44100)
	activateMode(t, v, VisMirror)
	if got := v.ModeName(); got != "Mirror" {
		t.Fatalf("ModeName() = %q, want Mirror", got)
	}
	if got := v.TickInterval(VisTickContext{Playing: true}); got != TickAnim {
		t.Fatalf("TickInterval(playing) = %v, want %v", got, TickAnim)
	}
}

func TestRenderMirrorHasPersistentAxisAndSymmetricBars(t *testing.T) {
	withPanelWidth(t, 24)
	v := NewVisualizer(44100)
	v.Rows = 5
	v.Mode = VisMirror

	frame := []rune(ansi.Strip(v.Render()))
	dotRows := v.Rows * 4
	dotCols := PanelWidth * 2
	span := dotCols * mirrorSpanPercent / 100
	span = min(dotCols, span-span%2)
	x0 := (dotCols - span) / 2
	axisY := dotRows / 2

	for x := x0; x < x0+span; x++ {
		if !mirrorDotAt(frame, PanelWidth, x, axisY) {
			t.Fatalf("axis missing at x=%d", x)
		}
	}
	for x := range dotCols {
		for offset := 1; axisY-offset >= 0 && axisY+offset < dotRows; offset++ {
			if mirrorDotAt(frame, PanelWidth, x, axisY-offset) != mirrorDotAt(frame, PanelWidth, x, axisY+offset) {
				t.Fatalf("bar is not vertically symmetric at x=%d, offset=%d", x, offset)
			}
		}
	}
	if got := mirrorDotCount(frame); got <= span {
		t.Fatalf("dot count = %d, want more than the %d-dot axis", got, span)
	}
	quietDots := mirrorDotCount(frame)
	v.bands = uniformBands(1)
	if loudDots := mirrorDotCount([]rune(ansi.Strip(v.Render()))); loudDots <= quietDots {
		t.Fatalf("loud dot count = %d, want more than quiet count %d", loudDots, quietDots)
	}
}

func mirrorDotAt(frame []rune, cols, x, y int) bool {
	row := y / 4
	col := x / 2
	return frame[row*(cols+1)+col]&brailleBit[y%4][x%2] != 0
}

func mirrorDotCount(frame []rune) int {
	count := 0
	for _, glyph := range frame {
		if glyph < '\u2800' || glyph > '\u28ff' {
			continue
		}
		for _, row := range brailleBit {
			for _, bit := range row {
				if glyph&bit != 0 {
					count++
				}
			}
		}
	}
	return count
}
