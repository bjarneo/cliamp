package ui

import (
	"math"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
)

func fifthsSine(hz, amp, sr float64, n int) []float64 {
	out := make([]float64, n)
	for i := range out {
		out[i] = amp * math.Sin(2*math.Pi*hz*float64(i)/sr)
	}
	return out
}

func TestFifthsStepOrder(t *testing.T) {
	tests := []struct {
		pc, step int
	}{
		{0, 0},   // C at the top
		{7, 1},   // G
		{2, 2},   // D
		{6, 6},   // F# opposite C
		{5, 11},  // F just left of C
		{10, 10}, // A#
	}
	for _, tt := range tests {
		if got := fifthsStep[tt.pc]; got != tt.step {
			t.Errorf("fifthsStep[%s] = %d, want %d", fifthsLabels[tt.pc], got, tt.step)
		}
	}
}

func TestFifthsAnalyzeFindsNote(t *testing.T) {
	const sr = 44100.0
	tests := []struct {
		name string
		hz   float64
		note int // semitones from C0
	}{
		{"A1 bass", 55, 12 + 9},
		{"E2", 82.40688922821748, 24 + 4},
		{"A4", 440, 48 + 9},
		{"C#6", 1108.7305239074883, 72 + 1},
		{"G7", 3135.9634878539946, 84 + 7},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v := NewVisualizer(sr)
			d := newFifthsDriver().(*fifthsDriver)
			d.analyze(v, fifthsSine(tt.hz, 0.5, sr, fifthsFFTSize), TickAnalyze.Seconds())
			best := 0
			for i, l := range d.target {
				if l > d.target[best] {
					best = i
				}
			}
			if best != tt.note {
				t.Fatalf("loudest note = %d, want %d", best, tt.note)
			}
			if d.target[best] < 0.9 {
				t.Errorf("level of the played note = %.2f, want near 1", d.target[best])
			}
			for _, nb := range []int{tt.note - 1, tt.note + 1} {
				if d.target[nb] >= d.target[tt.note]-0.1 {
					t.Errorf("neighbour %d at %.2f is not clearly below %.2f", nb, d.target[nb], d.target[tt.note])
				}
			}
		})
	}
}

func TestFifthsAnalyzeSilence(t *testing.T) {
	v := NewVisualizer(44100)
	d := newFifthsDriver().(*fifthsDriver)
	d.analyze(v, fifthsSine(440, 0.5, 44100, fifthsFFTSize), TickAnalyze.Seconds())
	for _, samples := range [][]float64{nil, make([]float64, fifthsFFTSize)} {
		d.analyze(v, samples, TickAnalyze.Seconds())
		for i, l := range d.target {
			if l != 0 {
				t.Fatalf("target[%d] = %v in silence, want 0", i, l)
			}
		}
	}
}

func TestFifthsLayout(t *testing.T) {
	tests := []struct {
		name       string
		rows, cols int
		labels     bool
	}{
		{"default panel", 7, 80, false},
		{"full screen", 40, 120, true},
		{"tall and narrow", 40, 25, false},
		{"small square", 9, 48, false},
		{"narrow", 3, 13, false},
		{"one row", 1, 40, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dotRows, dotCols := tt.rows*4, tt.cols*2
			c, labels, ok := fifthsLayout(dotRows, dotCols)
			if !ok || labels != tt.labels {
				t.Fatalf("ok %v, labels %v; want true, %v", ok, labels, tt.labels)
			}
			if c.ry > c.rx*fifthsTilt+1e-9 || c.step <= 0 {
				t.Errorf("ring %.1f by %.1f, step %.2f: not a ring seen from above", c.rx, c.ry, c.step)
			}
			top := c.baseY - float64(fifthsOctaves-1)*c.step - c.ry
			bottom := c.baseY + c.ry
			if c.cx-c.rx < -0.5 || c.cx+c.rx > float64(dotCols)-0.5 || top < -0.5 || bottom > float64(dotRows)-0.5 {
				t.Errorf("cylinder %+v leaves the %dx%d dot panel", c, dotCols, dotRows)
			}
		})
	}
}

func TestFifthsFarSideIsFaint(t *testing.T) {
	v := NewVisualizer(44100)
	v.Mode, v.Rows, v.Cols = VisFifths, 7, 80
	d := v.syncDriverMode().(*fifthsDriver) // runs OnEnter first
	d.level[4*12+0] = 1                     // C4, at the back
	d.level[4*12+6] = 1                     // F#4, at the front
	frame := v.Render()
	for _, tc := range []struct {
		name  string
		style int
		want  bool
	}{
		{"C faint", fifthsStyleFar + 0, true},
		{"C at full strength", fifthsStylePC + 0, false},
		{"F# at full strength", fifthsStylePC + 6, true},
		{"F# faint", fifthsStyleFar + 6, false},
	} {
		if got := strings.Contains(frame, fifthsANSI[tc.style].prefix); got != tc.want {
			t.Errorf("%s: drawn %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestFifthsRenderFillsFrame(t *testing.T) {
	sizes := []struct{ rows, cols int }{{7, 80}, {40, 120}, {3, 13}, {1, 40}, {16, 140}}
	for _, sz := range sizes {
		v := NewVisualizer(44100)
		v.Mode, v.Rows, v.Cols = VisFifths, sz.rows, sz.cols
		d := v.syncDriverMode().(*fifthsDriver) // runs OnEnter first
		for i := range d.level {
			d.level[i] = float64(i%7) / 6
		}
		lines := strings.Split(v.Render(), "\n")
		if len(lines) != sz.rows {
			t.Fatalf("%dx%d: %d lines, want %d", sz.cols, sz.rows, len(lines), sz.rows)
		}
		for _, line := range lines {
			if w := ansi.StringWidth(line); w != sz.cols {
				t.Errorf("%dx%d: line width %d", sz.cols, sz.rows, w)
			}
		}
		// The note colours, not just the dim rim marks, must reach the frame.
		frame := v.Render()
		drawn := false
		for pc := range 12 {
			drawn = drawn || strings.Contains(frame, fifthsANSI[fifthsStylePC+pc].prefix)
		}
		if !drawn {
			t.Errorf("%dx%d: no note drawn", sz.cols, sz.rows)
		}
	}
}

func TestFifthsSettlesWhenStopped(t *testing.T) {
	v := NewVisualizer(44100)
	v.Rows, v.Cols = 7, 80
	d := newFifthsDriver().(*fifthsDriver)
	for i := range d.level {
		d.level[i], d.target[i] = 1, 1
	}
	now := time.Unix(0, 0)
	for range 400 {
		now = now.Add(TickAnim)
		d.Tick(v, VisTickContext{Now: now})
	}
	if !d.pauseSettled() {
		t.Fatal("levels did not settle after stopping")
	}
	if got := d.TickInterval(v, VisTickContext{}); got != TickSlow {
		t.Errorf("TickInterval at rest = %v, want %v", got, TickSlow)
	}
}

func TestFifthsTurnsOnlyWhilePlaying(t *testing.T) {
	v := NewVisualizer(44100)
	v.Rows, v.Cols = 7, 80
	d := newFifthsDriver().(*fifthsDriver)
	now := time.Unix(0, 0)
	tick := func(playing bool, n int) {
		for range n {
			now = now.Add(TickAnim)
			d.Tick(v, VisTickContext{Now: now, Playing: playing})
		}
	}
	tick(false, 100)
	if d.turn != 0 {
		t.Fatalf("turned %.3f rad while stopped", d.turn)
	}
	tick(true, 63) // about a second
	want := 2 * math.Pi / fifthsTurnSeconds
	if d.turn < want*0.9 || d.turn > want*1.1 {
		t.Errorf("turned %.4f rad in a second of playback, want about %.4f", d.turn, want)
	}
	held := d.turn
	tick(false, 100)
	if d.turn != held {
		t.Errorf("kept turning after playback stopped")
	}
}
