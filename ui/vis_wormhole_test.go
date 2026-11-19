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

var wormholePlaying = VisTickContext{Playing: true}

func newWormholeTest(cols, rows int) (*wormholeDriver, *Visualizer) {
	v := NewVisualizer(44100)
	v.Cols, v.Rows = cols, rows
	return newWormholeDriver().(*wormholeDriver), v
}

// wormholeTick sets raw and smoothed bands and ticks n times.
func wormholeTick(d *wormholeDriver, v *Visualizer, ctx VisTickContext, ticks int, bands []float64) {
	copy(v.bands, bands)
	v.smoothedBands = append(v.smoothedBands[:0], bands...)
	for range ticks {
		d.Tick(v, ctx)
	}
}

// wormholeMix: bands 0-2 bass, 3-5 mids, rest treble.
func wormholeMix(bass, mids, treble float64) []float64 {
	bands := uniformBands(treble)
	for i := range 6 {
		bands[i] = mids
		if i < 3 {
			bands[i] = bass
		}
	}
	return bands
}

func wormholeBands(bass, rest float64) []float64 { return wormholeMix(bass, rest, rest) }

// wormholeKick is a loud bass on the kick and a quiet one between.
func wormholeKick(_ float64, kick bool) []float64 {
	if kick {
		return wormholeBands(0.9, 0.3)
	}
	return wormholeBands(0.3, 0.3)
}

// wormholePlayTempo plays kicks at bpm for secs seconds, two loud ticks a beat.
func wormholePlayTempo(d *wormholeDriver, v *Visualizer, bpm, secs float64) {
	wormholePlay(d, v, bpm, secs, wormholeKick, nil)
}

// wormholePlay ticks for secs seconds. bands gives the levels of the tick at
// sec and is told whether a kick at bpm falls on it; after runs after each tick.
func wormholePlay(d *wormholeDriver, v *Visualizer, bpm, secs float64, bands func(sec float64, kick bool) []float64, after func(sec float64)) {
	period, dt := 60/bpm, TickRawSample.Seconds()
	for tick := range int(secs/dt + 1e-9) {
		sec := float64(tick) * dt
		wormholeTick(d, v, wormholePlaying, 1, bands(sec, math.Mod(sec, period) < 2*dt-1e-9))
		if after != nil {
			after(sec)
		}
	}
}

// wormholePlayBeat plays one beat at 240 BPM.
func wormholePlayBeat(d *wormholeDriver, v *Visualizer) {
	wormholePlayTempo(d, v, 240, 0.25)
}

// wormholeShare: distance of dot i from the centre, normalised to the panel.
func wormholeShare(d *wormholeDriver, i int) float64 {
	w, h := float64(d.grid.dotCols), float64(d.grid.dotRows)
	ex := (float64(i%d.grid.dotCols) + 0.5 - w/2) / (w / 2)
	ey := (float64(i/d.grid.dotCols) + 0.5 - h/2) / (h / 2)
	return math.Hypot(ex, ey)
}

func wormholeWaving(d *wormholeDriver) bool {
	return slices.Contains(d.grid.cells, wormholeBeat) || slices.Contains(d.grid.cells, wormholeHot)
}

func TestWormholeFitsPanel(t *testing.T) {
	for _, size := range []struct{ cols, rows int }{{1, 1}, {4, 5}, {13, 3}, {76, 7}, {140, 40}} {
		t.Run(fmt.Sprintf("%dx%d", size.cols, size.rows), func(t *testing.T) {
			v := NewVisualizer(44100)
			v.Cols, v.Rows, v.Mode = size.cols, size.rows, VisWormhole
			for range 20 {
				v.Tick(wormholePlaying)
			}
			out := v.Render()
			lines := strings.Split(out, "\n")
			if len(lines) != size.rows {
				t.Fatalf("got %d rows, want %d", len(lines), size.rows)
			}
			for _, line := range lines {
				if got := lipgloss.Width(line); got != size.cols {
					t.Fatalf("got width %d, want %d", got, size.cols)
				}
			}
			for _, r := range stripSGR(out) {
				if r != '\n' && (r < '⠀' || r > '⣿') {
					t.Fatalf("non-Braille glyph %q", r)
				}
			}
		})
	}
}

// After 1/10 lap every ring has moved into its neighbour's place.
func TestWormholeRings(t *testing.T) {
	d, v := newWormholeTest(140, 40)
	d.travel = 2.03
	first := d.Render(v)
	d.travel = 2.08
	if d.Render(v) == first {
		t.Fatal("the rings did not move")
	}
	d.travel = 2.13
	if d.Render(v) != first {
		t.Fatal("the rings did not move on by one place in a tenth of a lap")
	}
}

func TestWormholeMouth(t *testing.T) {
	for _, size := range []struct{ cols, rows int }{{76, 7}, {110, 15}, {180, 40}} {
		t.Run(fmt.Sprintf("%dx%d", size.cols, size.rows), func(t *testing.T) {
			d, v := newWormholeTest(size.cols, size.rows)
			d.travel = 2.53
			d.Render(v)
			var mouth, rim int
			for i, tier := range d.grid.cells {
				if tier == 0 {
					continue
				}
				if r := wormholeShare(d, i); r < 0.6*wormholeMouth {
					mouth++
				} else if r > 0.9 {
					rim++
				}
			}
			if mouth != 0 || rim == 0 {
				t.Fatalf("%d dots in the mouth and %d at the edge, want a dark mouth and a wall to the edge", mouth, rim)
			}
		})
	}
}

func TestWormholeKick(t *testing.T) {
	d, v := newWormholeTest(110, 15)
	wormholeTick(d, v, wormholePlaying, 400, uniformBands(0.3))
	last := func() wormholeWave { return d.waves[(d.nextWave+wormholeWaves-1)%wormholeWaves] }
	// Kicks as strong as the ones before them glow, but not hot.
	for range 8 {
		wormholeTick(d, v, wormholePlaying, 2, wormholeBands(0.6, 0.3))
		if s := last().strength; s == 0 || s >= wormholeWaveHot {
			t.Fatalf("an ordinary kick sent a wave of strength %.2f, want above 0 and below %.2f", s, wormholeWaveHot)
		}
		d.Render(v)
		if !wormholeWaving(d) || slices.Contains(d.grid.cells, wormholeHot) {
			t.Fatal("the wave of an ordinary kick does not glow, or glows hot")
		}
		wormholeTick(d, v, wormholePlaying, 18, uniformBands(0.3))
	}
	// A kick that stands out glows hot.
	wormholeTick(d, v, wormholePlaying, 2, wormholeBands(1, 0.3))
	wave := last()
	if wave.strength < wormholeWaveHot {
		t.Fatalf("a hard hit sent a wave of strength %.2f, want at least %.2f", wave.strength, wormholeWaveHot)
	}
	d.Render(v)
	if !slices.Contains(d.grid.cells, wormholeHot) {
		t.Fatal("the wave of a hard hit does not glow hot")
	}
	if front, ok := wave.front(d.clock); !ok || front > wormholeInner+1 {
		t.Fatalf("the front sits at inverse depth %.2f right after the beat, want it at the mouth", front)
	}
	wormholeTick(d, v, wormholePlaying, int(wormholeWaveTime/TickRawSample.Seconds())+2, uniformBands(0.3))
	d.Render(v)
	if wormholeWaving(d) {
		t.Fatal("the wave is still there after it reached the edge")
	}
}

func TestWormholeDrums(t *testing.T) {
	for _, tc := range []struct {
		name               string
		bands              []float64
		wave, rock, sparks bool
	}{
		{"steady", wormholeMix(0.3, 0.3, 0.3), false, false, false},
		{"bass", wormholeMix(0.9, 0.3, 0.3), true, false, false},
		{"snare", wormholeMix(0.3, 0.9, 0.3), false, true, false},
		{"hi-hat", wormholeMix(0.3, 0.3, 0.9), false, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d, v := newWormholeTest(110, 15)
			wormholeTick(d, v, wormholePlaying, 400, wormholeMix(0.3, 0.3, 0.3))
			wormholeTick(d, v, wormholePlaying, 2, tc.bands)
			wormholeTick(d, v, wormholePlaying, 4, wormholeMix(0.3, 0.3, 0.3))
			wave := slices.ContainsFunc(d.waves[:], func(w wormholeWave) bool { return w.strength > 0 })
			if wave != tc.wave || (d.rockTo != 0) != tc.rock || (d.struck > 0) != tc.sparks {
				t.Fatalf("wave %v, rock %v, sparks %v; want %v, %v, %v", wave, d.rockTo != 0, d.struck > 0, tc.wave, tc.rock, tc.sparks)
			}
		})
	}
}

// Without a beat there is no tempo: a steady level, however loud, stays at
// cruise. Silence stops the tunnel.
func TestWormholeCruise(t *testing.T) {
	for _, tc := range []struct{ level, want float64 }{{0, 0}, {0.5, wormholeCruise}, {0.9, wormholeCruise}} {
		d, v := newWormholeTest(76, 7)
		wormholeTick(d, v, wormholePlaying, 600, uniformBands(tc.level))
		if math.Abs(d.speed-tc.want) > 0.01 || d.sure != 0 {
			t.Fatalf("level %.1f: speed %.3f, tempo sure %.2f, want speed %.3f", tc.level, d.speed, d.sure, tc.want)
		}
		if still := d.twist == 0 && d.bendPhase == 0; still != (tc.level == 0) {
			t.Fatalf("level %.1f: twist %.3f, bend phase %.3f, want them to move only with sound", tc.level, d.twist, d.bendPhase)
		}
	}
}

// Sparks start at the top of the wall, where the rings show the treble.
func TestWormholeSparks(t *testing.T) {
	d, v := newWormholeTest(110, 15)
	wormholeTick(d, v, wormholePlaying, 400, wormholeMix(0.3, 0.3, 0.3))
	wormholeTick(d, v, wormholePlaying, 2, wormholeMix(0.3, 0.3, 0.9))
	d.Render(v)
	sparks := 0
	for i, tier := range d.grid.cells {
		if tier != wormholeBeat {
			continue
		}
		sparks++
		if row := i / d.grid.dotCols; row >= d.grid.dotRows/2 {
			t.Fatalf("a spark dot in row %d of %d, want the sparks in the upper half", row, d.grid.dotRows)
		}
	}
	if sparks == 0 {
		t.Fatal("a hi-hat struck no sparks")
	}
}

func TestWormholeTempo(t *testing.T) {
	for _, tc := range []struct{ bpm, want float64 }{
		{70, 70}, {120, 120}, {170, 170},
		{240, 120}, // folded down
		{50, 100},  // folded up
	} {
		t.Run(fmt.Sprint(tc.bpm), func(t *testing.T) {
			d, v := newWormholeTest(76, 7)
			wormholePlayTempo(d, v, tc.bpm, 8)
			if want := 60 / tc.want; math.Abs(d.beat-want) > TickRawSample.Seconds()+1e-9 || d.sure != 1 {
				t.Fatalf("beat %.3f s (sure %.2f), want %.3f s", d.beat, d.sure, want)
			}
		})
	}
}

func TestWormholeSpeed(t *testing.T) {
	speedAt := func(bpm float64) float64 {
		d, v := newWormholeTest(76, 7)
		wormholePlayTempo(d, v, bpm, 8)
		return d.speed
	}
	slow, mid, fast := speedAt(70), speedAt(120), speedAt(170)
	if !(slow < mid && mid < fast) || fast < 2*slow || fast > wormholeTopSpeed {
		t.Fatalf("speed %.3f at 70 BPM, %.3f at 120, %.3f at 170; want it to rise with the tempo, 170 at least twice 70", slow, mid, fast)
	}
	// One ring per beat, give or take the phase lock.
	if want := 2.0 / wormholeRings; math.Abs(mid-want) > wormholeLockMax*want {
		t.Fatalf("speed %.3f at 120 BPM, want about one ring per beat (%.3f)", mid, want)
	}
}

func TestWormholeTempoFades(t *testing.T) {
	d, v := newWormholeTest(76, 7)
	wormholePlayTempo(d, v, 170, 8)
	spiral := d.spiral
	if spiral <= 0 {
		t.Fatal("a steady beat did not twist the wall into a spiral")
	}
	for d.clock-d.lastKick+TickRawSample.Seconds() < wormholeSureWait {
		wormholeTick(d, v, wormholePlaying, 1, uniformBands(0.3))
		if d.sure != 1 {
			t.Fatalf("the tempo faded to %.2f %.2f s after the last kick", d.sure, d.clock-d.lastKick)
		}
	}
	wormholeTick(d, v, wormholePlaying, 400, uniformBands(0.3))
	if d.sure != 0 || math.Abs(d.speed-wormholeCruise) > 0.01 || d.spiral > 0.1*spiral {
		t.Fatalf("10 s after the kicks stopped: sure %.2f, speed %.3f, spiral %.3f; want 0, cruise and straight", d.sure, d.speed, d.spiral)
	}
}

func TestWormholeSpectralRings(t *testing.T) {
	d, _ := newWormholeTest(76, 7)
	bass, treble := [DefaultSpectrumBands]float64{1, 1, 1}, [DefaultSpectrumBands]float64{}
	for i := 6; i < DefaultSpectrumBands; i++ {
		treble[i] = 1
	}
	const bottom, top = 1.0, -1.0
	depth := func(ring int) float64 { return 1 - d.travel + float64(ring)/wormholeRings }

	d.travel, d.glow = 2, bass
	d.keepShapes()
	old := d.born
	if b, tp := d.spectrum(bottom, depth(old)), d.spectrum(top, depth(old)); b < 0.99 || tp > 0.01 {
		t.Fatalf("a ring born on the bass bulges %.2f at the bottom and %.2f at the top, want 1 and 0", b, tp)
	}
	d.travel, d.glow = 2.3, treble
	d.keepShapes()
	if d.born-old != 3 {
		t.Fatalf("%d rings born in 0.3 laps, want 3", d.born-old)
	}
	if b, tp := d.spectrum(bottom, depth(old)), d.spectrum(top, depth(old)); b < 0.99 || tp > 0.01 {
		t.Fatalf("the bass ring changed shape on its way out: %.2f at the bottom, %.2f at the top", b, tp)
	}
	if b, tp := d.spectrum(bottom, depth(d.born)), d.spectrum(top, depth(d.born)); b > 0.01 || tp < 0.99 {
		t.Fatalf("a ring born on the treble bulges %.2f at the bottom and %.2f at the top, want 0 and 1", b, tp)
	}
	// Rings behind the viewer have no shape left.
	if got := d.ringLevel(d.born-wormholeShapes, 0); got != 0 {
		t.Fatalf("a ring long gone bulges %.2f", got)
	}
}

func TestWormholeOutlines(t *testing.T) {
	// form runs 0..4 through the shapes, as Render splits it.
	outline := func(form, a float64) float64 {
		form = wormholeWrap(form, wormholeForms)
		return wormholeBlend(int(form), form-math.Floor(form), a)
	}
	for _, tc := range []struct {
		name       string
		form       float64
		corner, in float64 // angle of a corner, and of the narrowest point
		narrow     float64
	}{
		{"circle", 0, 0, math.Pi / 7, 1},
		{"hexagon", 1, 0, math.Pi / 6, math.Cos(math.Pi / 6)},
		{"square", 2, 0, math.Pi / 4, math.Cos(math.Pi / 4)},
		{"star", 3, 0, math.Pi / 5, 1 / (1 + 2*wormholeStarDepth)},
		{"circle again", 4, 0, math.Pi / 7, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := outline(tc.form, tc.corner); math.Abs(got-1) > 1e-9 {
				t.Fatalf("corner at %.3f, want 1", got)
			}
			if got := outline(tc.form, tc.in); math.Abs(got-tc.narrow) > 1e-9 {
				t.Fatalf("narrowest at %.3f, want %.3f", got, tc.narrow)
			}
		})
	}
	for form := -2.0; form < 6; form += 0.37 {
		for a := -10.0; a < 10; a += 0.41 {
			if r := outline(form, a); r > 1+1e-9 || r < 0.5 {
				t.Fatalf("form %.2f at angle %.2f has radius %.3f, want 0.5..1", form, a, r)
			}
		}
	}
}

func TestWormholeDrop(t *testing.T) {
	quiet, loud := uniformBands(0.08), uniformBands(0.5)
	for _, tc := range []struct {
		name string
		from []float64
		warp bool
	}{
		{"a steady loud song", loud, false},
		{"a drop", quiet, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d, v := newWormholeTest(110, 15)
			wormholeTick(d, v, wormholePlaying, 400, tc.from)
			peak := 0.0
			for range int(wormholeWarpLife / TickRawSample.Seconds()) {
				wormholeTick(d, v, wormholePlaying, 1, loud)
				peak = max(peak, d.warp)
			}
			if tc.warp != (peak > 0.9) || tc.warp != (d.jump == 1) {
				t.Fatalf("warp peaked at %.2f with %v shape jumps, want warp %v", peak, d.jump, tc.warp)
			}
			wormholeTick(d, v, wormholePlaying, 4, loud)
			if d.warp != 0 || d.speed > wormholeTopSpeed {
				t.Fatalf("warp %.2f and speed %.3f after the warp, want 0 and at most top speed", d.warp, d.speed)
			}
		})
	}
	// A second drop right after the first does not warp again.
	d, v := newWormholeTest(110, 15)
	wormholeTick(d, v, wormholePlaying, int(wormholeDropWarm/TickRawSample.Seconds()), quiet)
	for range 2 {
		wormholeTick(d, v, wormholePlaying, 40, quiet)
		wormholeTick(d, v, wormholePlaying, 40, loud)
	}
	if d.jump != 1 {
		t.Fatalf("two drops 2 s apart made %v warps, want 1", d.jump)
	}
}

// A track that rises out of silence is no drop, the first time or after a gap.
func TestWormholeTrackStart(t *testing.T) {
	d, v := newWormholeTest(110, 15)
	dt := TickRawSample.Seconds()
	for range 2 {
		wormholeTick(d, v, wormholePlaying, 40, uniformBands(0))
		wormholePlay(d, v, 128, 8, func(sec float64, kick bool) []float64 {
			rise := min(1, sec/0.3) // the smoothed bands rise out of silence
			if kick {
				return wormholeBands(0.9*rise, 0.3*rise)
			}
			return wormholeBands(0.3*rise, 0.3*rise)
		}, nil)
	}
	if d.jump != 0 {
		t.Fatalf("starting a track warped %v times, want none", d.jump)
	}
	// Before the averages know the song, even a real jump does not warp.
	d, v = newWormholeTest(110, 15)
	wormholeTick(d, v, wormholePlaying, int(wormholeDropWarm/2/dt), uniformBands(0.08))
	wormholeTick(d, v, wormholePlaying, 40, uniformBands(0.5))
	if d.jump != 0 {
		t.Fatal("a jump in the first seconds of a track warped")
	}
}

func TestWormholePause(t *testing.T) {
	for _, tc := range []struct {
		name   string
		ctx    VisTickContext
		atOnce bool
	}{
		{"a pause eases to rest", VisTickContext{Paused: true}, false},
		{"a stop halts at once", VisTickContext{}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d, v := newWormholeTest(76, 7)
			for range 3 {
				wormholePlayBeat(d, v)
			}
			if d.pauseSettled() {
				t.Fatal("a moving tunnel reported rest")
			}
			before, clock, phase, twist := d.speed, d.clock, d.bendPhase, d.twist
			wormholeTick(d, v, tc.ctx, 1, wormholeBands(0.9, 0.5))
			if tc.atOnce != (d.speed == 0) || d.speed >= before {
				t.Fatalf("speed %.3f after one tick, from %.3f", d.speed, before)
			}
			wormholeTick(d, v, tc.ctx, 60, wormholeBands(0.9, 0.5))
			if !d.pauseSettled() || d.clock != clock || d.bendPhase != phase || d.twist != twist {
				t.Fatal("the tunnel did not come to rest, or its clock, bend or twist ran on")
			}
			travel, frame := d.travel, d.Render(v)
			wormholeTick(d, v, tc.ctx, 5, wormholeBands(0.9, 0.5))
			if d.travel != travel || d.Render(v) != frame {
				t.Fatal("the tunnel moved at rest")
			}
		})
	}
}

// Suspend() sends an overlay tick (also while paused); it must not stop the flight.
func TestWormholeOverlay(t *testing.T) {
	d, v := newWormholeTest(76, 7)
	now := time.Unix(1700000000, 0)
	ctx := wormholePlaying
	ctx.Now = now
	wormholeTick(d, v, ctx, 1, uniformBands(0.5))
	speed, travel := d.speed, d.travel
	d.Tick(v, VisTickContext{OverlayActive: true})
	if d.speed != speed || d.travel != travel || !d.lastTick.IsZero() {
		t.Fatal("an overlay tick changed the flight or kept the clock")
	}
	ctx.Now = now.Add(time.Hour)
	wormholeTick(d, v, ctx, 1, uniformBands(0.5))
	if flown := d.travel - travel; flown <= 0 || flown > wormholeTopSpeed*TickFast.Seconds() {
		t.Fatalf("flew %.4f laps after the overlay, want one frame", flown)
	}
}

func TestWormholeTheme(t *testing.T) {
	t.Cleanup(func() { ApplyThemeColors(theme.Default()) })
	d, v := newWormholeTest(76, 7)
	d.travel = 2.53
	d.clock = wormholeWaveTime / 3
	d.waves[0] = wormholeWave{at: 0, strength: 1}
	d.waves[1] = wormholeWave{at: d.clock / 2, strength: 0.6}
	ApplyThemeColors(theme.Default())
	terminalFrame := d.Render(v)
	for _, th := range []theme.Theme{theme.Default(), testTheme, theme.Default()} {
		t.Run(th.Name, func(t *testing.T) {
			ApplyThemeColors(th)
			p := PaletteFor(th)
			for i, c := range [...]color.Color{p.Dim, p.Text, p.SpectrumMid, p.SpectrumHigh} {
				var sb, run strings.Builder
				run.WriteString("⠂")
				flushWormholeRun(&sb, &run, i+1)
				want := lipgloss.NewStyle().Foreground(c).Render("⠂")
				if got := sb.String(); got != want {
					t.Errorf("style [%d] = %q, want %q", i, got, want)
				}
			}
			frame := d.Render(v)
			if stripSGR(frame) != stripSGR(terminalFrame) {
				t.Fatal("changing the theme changed the tunnel")
			}
			if th.IsDefault() {
				if frame != terminalFrame {
					t.Fatal("returning to the default theme did not restore terminal colors")
				}
				if strings.Contains(frame, "38;2;") {
					t.Fatal("the default theme did not use the terminal's ANSI palette")
				}
			} else if frame == terminalFrame {
				t.Fatal("changing the theme did not recolor the tunnel")
			}
			if strings.Contains(frame, "48;2;") || strings.Contains(frame, "48;5;") {
				t.Fatal("the visualizer painted a background")
			}
		})
	}
}

// Turning the bass up raises its floor between the kicks; the kicks must
// still count, or the waves and the tempo vanish until the range catches up.
func TestWormholeBassBoost(t *testing.T) {
	d, v := newWormholeTest(110, 15)
	kicks, last := 0, d.lastKick
	wormholePlay(d, v, 128, 16, func(sec float64, kick bool) []float64 {
		bass := 0.5
		if sec > 8 {
			bass = 1.4
		}
		if kick {
			return wormholeBands(bass, 0.3)
		}
		return wormholeBands(0.6*bass, 0.3)
	}, func(sec float64) {
		if sec > 8 && d.lastKick != last {
			kicks++
		}
		last = d.lastKick
		if sec > 4 && d.sure < 1 {
			t.Fatalf("the tempo faded to %.2f at %.1f s", d.sure, sec)
		}
	})
	if want := 8 * 128 / 60; kicks < want-1 {
		t.Fatalf("%d kicks counted in the 8 s after the boost, want %d", kicks, want)
	}
}

func BenchmarkWormholeFrame(b *testing.B) {
	for _, size := range []struct{ cols, rows int }{{76, 7}, {140, 40}} {
		b.Run(fmt.Sprintf("%dx%d", size.cols, size.rows), func(b *testing.B) {
			d, v := newWormholeTest(size.cols, size.rows)
			d.Render(v)
			b.ReportAllocs()
			for b.Loop() {
				d.Tick(v, wormholePlaying)
				d.Render(v)
			}
		})
	}
}
