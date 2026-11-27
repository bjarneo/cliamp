package ui

import (
	"fmt"
	"image/color"
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

func TestEtherealFollowsThemeColors(t *testing.T) {
	t.Cleanup(func() { ApplyThemeColors(theme.Default()) })
	v := NewVisualizer(44100)
	v.Cols, v.Rows = 76, 7
	d := &etherealDriver{}
	ApplyThemeColors(theme.Default())
	terminalFrame := d.Render(v)
	for _, th := range []theme.Theme{theme.Default(), testTheme, theme.Default()} {
		t.Run(th.Name, func(t *testing.T) {
			ApplyThemeColors(th)
			p := PaletteFor(th)
			colors := [...]color.Color{p.Dim, p.SpectrumLow, p.SpectrumMid, p.SpectrumHigh, p.Text, p.Accent, p.Text}
			for i, c := range colors {
				var sb strings.Builder
				w := styleRunWriter{sb: &sb}
				w.switchStyle(etherealTierStyle(i + 1))
				w.writeString("⠂")
				w.flush()
				want := lipgloss.NewStyle().Foreground(c).Render("⠂")
				if got := sb.String(); got != want {
					t.Errorf("style [%d] = %q, want %q", i, got, want)
				}
			}
			frame := d.Render(v)
			if stripSGR(frame) != stripSGR(terminalFrame) {
				t.Fatal("changing the theme changed the clouds or stars")
			}
			if th.IsDefault() {
				if frame != terminalFrame {
					t.Fatal("returning to the default theme did not restore terminal colors")
				}
				if strings.Contains(frame, "38;2;") {
					t.Fatal("the default theme did not use the terminal's ANSI palette")
				}
			} else if frame == terminalFrame {
				t.Fatal("changing the theme did not recolor the nebula")
			}
		})
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

func TestEtherealRenderMatchesCloudFormula(t *testing.T) {
	cases := []struct {
		name            string
		cols, rows      int
		elapsed         float64
		bass, mid, high float64
	}{
		{"rest", 48, 9, 0, 0, 0, 0},
		{"drifted", 60, 12, 7.5, 0.65, 0.4, 0.2},
		{"loud", 40, 8, 19.25, 1, 1, 0},
		{"tiny", 3, 1, 2.1, 0.3, 0.8, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			v := NewVisualizer(44100)
			v.Cols, v.Rows = tc.cols, tc.rows
			v.bands = []float64{tc.bass, tc.mid, tc.high}
			d := &etherealDriver{elapsed: tc.elapsed}
			d.Render(v)

			dotRows, dotCols := tc.rows*4, tc.cols*2
			want := brailleGrid{styleFor: etherealTierStyle, keepRunOnEmpty: true}
			want.ensure(dotRows, dotCols)
			for y := range dotRows {
				yf := float64(y) / float64(dotRows)
				for x := range dotCols {
					xf := (float64(x) + 0.5) / float64(dotCols)
					light := etherealCloud(xf, yf, tc.elapsed, tc.bass, tc.mid)
					if light < 0.10 || scatterHash(0, y, x, 0) > (light-0.08)*1.5 {
						continue
					}
					tier := int8(1)
					switch {
					case light >= 0.70:
						tier = 5
					case light >= 0.52:
						tier = 4
					case light >= 0.35:
						tier = 3
					case light >= 0.20:
						tier = 2
					}
					want.cells[y*dotCols+x] = tier
				}
			}
			for _, star := range d.stars {
				glow := 0.5 + 0.5*math.Sin(tc.elapsed*star.speed+star.phase)
				if glow+tc.high*0.35 <= 0.36 {
					continue
				}
				tier := int8(etherealStarDim)
				if glow+tc.high*0.25 > 0.78 {
					tier = etherealStarCool
				}
				if glow+tc.high*0.15 > 1.02 {
					tier = etherealStarLit
					for _, offset := range [4][2]int{{-1, 0}, {1, 0}, {0, -1}, {0, 1}} {
						want.set(star.x+offset[0], star.y+offset[1], tier)
					}
				}
				want.set(star.x, star.y, tier)
			}
			if !slices.Equal(d.grid.cells, want.cells) {
				t.Fatalf("grid cells differ from etherealCloud reference")
			}
		})
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
