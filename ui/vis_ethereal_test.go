package ui

import (
	"fmt"
	"math"
	"slices"
	"strings"
	"testing"
	"time"

	"charm.land/lipgloss/v2"

	"github.com/bjarneo/cliamp/theme"
)

func TestEtherealFitsPanel(t *testing.T) {
	for _, size := range []struct{ cols, rows int }{{1, 1}, {4, 5}, {13, 3}, {76, 7}, {140, 40}} {
		t.Run(fmt.Sprintf("%dx%d", size.cols, size.rows), func(t *testing.T) {
			v := NewVisualizer(44100)
			v.Cols, v.Rows, v.Mode = size.cols, size.rows, VisEthereal
			v.Tick(VisTickContext{Playing: true})
			lines := strings.Split(v.Render(), "\n")
			if len(lines) != size.rows {
				t.Fatalf("got %d rows, want %d", len(lines), size.rows)
			}
			for _, line := range lines {
				if got := lipgloss.Width(line); got != size.cols {
					t.Fatalf("got width %d, want %d", got, size.cols)
				}
			}
		})
	}
}

func TestEtherealCloudRespondsToAudio(t *testing.T) {
	for _, tc := range []struct {
		name      string
		bass, mid float64
	}{{"bass", 1, 0}, {"mids", 0, 1}, {"both", 1, 1}} {
		t.Run(tc.name, func(t *testing.T) {
			quiet, loud := 0.0, 0.0
			for row := range 60 {
				for col := range 120 {
					x, y := float64(col)/120, float64(row)/60
					rest := etherealCloud(x, y, 3, 0, 0)
					active := etherealCloud(x, y, 3, tc.bass, tc.mid)
					if rest < 0 || active < rest || active > 1 {
						t.Fatalf("cloud at (%.2f, %.2f): rest %.3f, active %.3f", x, y, rest, active)
					}
					quiet += rest
					loud += active
				}
			}
			if quiet == 0 || loud <= quiet*11/10 {
				t.Fatalf("total cloud light: quiet %.3f, active %.3f; want visible rest and an audio swell", quiet, loud)
			}
		})
	}
}

func TestEtherealStarsRespondToTreble(t *testing.T) {
	v := NewVisualizer(44100)
	v.Cols, v.Rows = 100, 20
	d := &etherealDriver{}
	stars := func() int {
		count := 0
		for _, tier := range d.grid.cells {
			if tier >= etherealStarCool {
				count++
			}
		}
		return count
	}
	d.Render(v)
	quiet := stars()
	v.bands[len(v.bands)-1] = 1
	v.bands[len(v.bands)-2] = 1
	v.bands[len(v.bands)-3] = 1
	v.bands[len(v.bands)-4] = 1
	d.Render(v)
	loud := stars()
	if loud <= quiet || !slices.Contains(d.grid.cells, etherealStarLit) {
		t.Fatalf("treble did not brighten the stars: quiet %d, loud %d", quiet, loud)
	}
}

func TestEtherealRenderIsStable(t *testing.T) {
	v := NewVisualizer(44100)
	v.Cols, v.Rows = 76, 7
	d := &etherealDriver{}
	first := d.Render(v)
	if again := d.Render(v); again != first {
		t.Fatal("render advanced the animation")
	}
	other := &etherealDriver{}
	if again := other.Render(v); again != first || !slices.Equal(d.stars, other.stars) {
		t.Fatal("the same input produced different clouds or stars")
	}
	d.elapsed = 5
	if later := d.Render(v); later == first {
		t.Fatal("the nebula did not drift over five seconds")
	}
	// Resizing away and back keeps the field deterministic, not accumulated noise.
	v.Cols, v.Rows = 13, 3
	d.Render(v)
	v.Cols, v.Rows, d.elapsed = 76, 7, 0
	if again := d.Render(v); again != first {
		t.Fatal("resizing changed the seeded field")
	}
}

func TestEtherealClock(t *testing.T) {
	start := time.Unix(1700000000, 0)
	for _, interval := range []time.Duration{TickFast, TickAnim, time.Second / 60} {
		t.Run(interval.String(), func(t *testing.T) {
			v := NewVisualizer(44100)
			d := &etherealDriver{}
			steps := int(time.Second / interval)
			for i := 0; i <= steps; i++ {
				d.Tick(v, VisTickContext{Now: start.Add(time.Duration(i) * interval), Playing: true})
			}
			want := TickFast.Seconds() + (time.Duration(steps) * interval).Seconds()
			if math.Abs(d.elapsed-want) > 1e-9 {
				t.Fatalf("elapsed = %.9f, want %.9f", d.elapsed, want)
			}
			before := d.elapsed
			d.Tick(v, VisTickContext{Now: d.lastTick, Playing: true})
			if d.elapsed != before {
				t.Fatal("a duplicate timestamp advanced the animation")
			}
		})
	}
}

func TestEtherealSuspendsClock(t *testing.T) {
	for _, tc := range []struct {
		name string
		ctx  VisTickContext
	}{
		{"paused", VisTickContext{Paused: true}},
		{"stopped", VisTickContext{}},
		{"overlay", VisTickContext{Playing: true, OverlayActive: true}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			v := NewVisualizer(44100)
			d := &etherealDriver{}
			now := time.Unix(1700000000, 0)
			d.Tick(v, VisTickContext{Now: now, Playing: true})
			before := d.elapsed
			tc.ctx.Now = now.Add(time.Second)
			d.Tick(v, tc.ctx)
			if d.elapsed != before || !d.lastTick.IsZero() {
				t.Fatal("inactive playback advanced the animation or kept its clock")
			}
			d.Tick(v, VisTickContext{Now: now.Add(time.Hour), Playing: true})
			if math.Abs(d.elapsed-before-TickFast.Seconds()) > 1e-9 {
				t.Fatal("resuming caught up the hidden interval")
			}
			d.OnEnter(v)
			if d.elapsed != 0 || !d.lastTick.IsZero() {
				t.Fatal("re-entering the mode did not reset its clock")
			}
		})
	}
}

func TestEtherealKeepsItsPalette(t *testing.T) {
	t.Cleanup(func() { ApplyThemeColors(theme.Default()) })
	v := NewVisualizer(44100)
	v.Cols, v.Rows = 76, 7
	d := &etherealDriver{}
	ApplyThemeColors(theme.Default())
	before := d.Render(v)
	ApplyThemeColors(testTheme)
	if got := d.Render(v); got != before {
		t.Fatal("changing the UI theme recoloured the nebula")
	}
	for i, c := range etherealColors {
		var sb, run strings.Builder
		run.WriteString("⠂")
		flushEtherealRun(&sb, &run, i+1)
		want := lipgloss.NewStyle().Foreground(lipgloss.Color(c)).Render("⠂")
		if got := sb.String(); got != want {
			t.Fatalf("style [%d] = %q, want %q", i, got, want)
		}
	}
}

func TestEtherealUsesFlameStyleDots(t *testing.T) {
	v := NewVisualizer(44100)
	v.Cols, v.Rows = 76, 7
	d := &etherealDriver{}
	out := d.Render(v)
	for _, r := range stripSGR(out) {
		if r != '\n' && (r < '\u2800' || r > '\u28ff') {
			t.Fatalf("non-Braille cloud pixel %q", r)
		}
	}
	if strings.Contains(out, "48;2;") || strings.Contains(out, "48;5;") {
		t.Fatal("the visualizer painted a solid background")
	}
	if !slices.Contains(d.grid.cells, int8(0)) || !slices.Contains(d.grid.cells, int8(3)) {
		t.Fatal("want open sky and visible cloud dots")
	}
}

func BenchmarkEtherealFrame(b *testing.B) {
	for _, size := range []struct{ cols, rows int }{{76, 7}, {140, 40}} {
		b.Run(fmt.Sprintf("%dx%d", size.cols, size.rows), func(b *testing.B) {
			v := NewVisualizer(44100)
			v.Cols, v.Rows = size.cols, size.rows
			d := &etherealDriver{}
			d.Render(v)
			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				d.Tick(v, VisTickContext{Playing: true})
				d.Render(v)
			}
		})
	}
}
