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
	"github.com/charmbracelet/x/ansi"

	"github.com/bjarneo/cliamp/theme"
)

func yinYangAt(rows, cols int) (*yinYangDriver, *Visualizer) {
	v := NewVisualizer(44100)
	v.Rows, v.Cols = rows, cols
	v.Mode = VisYinYang
	d := v.syncDriverMode().(*yinYangDriver)
	d.layout(cols*2, rows*4)
	return d, v
}

func yinYangDrawAlone(d *yinYangDriver, i int) []int8 {
	d.px = make([]int8, d.w*d.h)
	d.drawKoi(&d.koi[i], yinYangPatterns[i])
	return d.px
}

func yinYangHits(bands []float64, from, to int, low, high float64, hit bool) {
	for i := from; i < min(to, len(bands)); i++ {
		bands[i] = low
		if hit {
			bands[i] = high
		}
	}
}

func yinYangCount(px []int8, tag int8) (n int) {
	for _, p := range px {
		if p == tag {
			n++
		}
	}
	return n
}

func yinYangSameMotion(a, b [2]yinYangKoi) bool {
	for i := range a {
		x, y := a[i], b[i]
		if x.joints != y.joints || x.offset != y.offset || x.phase != y.phase ||
			x.stroke != y.stroke || x.flickPower != y.flickPower || x.flick != y.flick {
			return false
		}
	}
	return true
}

func TestYinYangTrackMatchesThePanel(t *testing.T) {
	for _, tt := range []struct {
		rows, cols int
		strip      bool
	}{{7, 120, true}, {5, 80, true}, {7, 240, true}, {40, 120, false}, {20, 40, false}} {
		t.Run(fmt.Sprintf("%dx%d", tt.cols, tt.rows), func(t *testing.T) {
			d, _ := yinYangAt(tt.rows, tt.cols)
			if d.strip != tt.strip {
				t.Fatalf("strip = %v, want %v", d.strip, tt.strip)
			}
			minY, maxY := math.Inf(1), math.Inf(-1)
			for i := range 720 {
				a := float64(i) * 2 * math.Pi / 720
				p := d.trackPoint(a)
				if d.strip {
					next := d.trackPoint(a + 2*math.Pi)
					if math.Abs(next.x-p.x-d.lap) > 1e-9 || math.Abs(next.y-p.y) > 1e-9 {
						t.Fatal("the wave does not repeat every lap")
					}
					p.x = 0
				}
				if p.x < 0 || p.x >= float64(d.w) || p.y < 0 || p.y >= float64(d.h) {
					t.Fatalf("head at %+v is outside the panel", p)
				}
				minY, maxY = min(minY, p.y), max(maxY, p.y)
			}
			if d.strip && maxY-minY < float64(d.h)*0.15 {
				t.Error("the strip path has no visible rise and fall")
			}
		})
	}
}

// Over two laps the koi keep a steady speed, their spacing and their length,
// and strip koi only ever swim forwards.
func TestYinYangSwimming(t *testing.T) {
	for _, sz := range []struct{ rows, cols int }{{5, 80}, {7, 120}, {7, 240}, {40, 120}} {
		t.Run(fmt.Sprintf("%dx%d", sz.cols, sz.rows), func(t *testing.T) {
			d, _ := yinYangAt(sz.rows, sz.cols)
			d.loud = 0
			bands := uniformBands(0.5)
			seg := yinYangSeg * d.size
			length := float64(yinYangJoints-1) * seg
			minSpeed, maxSpeed := math.Inf(1), 0.0
			for frame := 0; d.angle < 4*math.Pi; frame++ {
				if frame > 20000 {
					t.Fatal("the koi did not complete two laps")
				}
				prev := d.koi[0]
				d.advance(bands, true)
				k := d.koi[0]
				for j := range k.joints {
					if dx := k.joints[j].x - prev.joints[j].x; d.strip && (dx <= 0 || dx > seg) {
						t.Fatalf("frame %d: joint %d reversed or jumped by %.4f", frame, j, dx)
					}
					if j > 0 {
						a, b := k.joints[j-1], k.joints[j]
						if got := math.Hypot(b.x-a.x, b.y-a.y); math.Abs(got-seg) > 1e-9 {
							t.Fatalf("frame %d: joint %d is %.4f from the one before it", frame, j, got)
						}
					}
				}
				if d.angle < 2*math.Pi {
					continue
				}
				head, tail := k.joints[0], k.joints[yinYangJoints-1]
				speed := math.Hypot(head.x-prev.joints[0].x, head.y-prev.joints[0].y)
				minSpeed, maxSpeed = min(minSpeed, speed), max(maxSpeed, speed)
				if span := math.Hypot(head.x-tail.x, head.y-tail.y); span < length*0.85 {
					t.Fatalf("frame %d: a %.2f-long koi folded to %.2f", frame, length, span)
				}
			}
			if minSpeed < maxSpeed*0.95 {
				t.Errorf("speed falls from %.4f to %.4f during a lap", maxSpeed, minSpeed)
			}
		})
	}
}

// A koi leaving the strip on the right is back on the left within about a
// pixel's travel, and shows on both edges only for a moment.
func TestYinYangStripKoiComeStraightBack(t *testing.T) {
	for _, sz := range []struct{ rows, cols int }{{3, 56}, {5, 80}, {7, 120}, {7, 45}} {
		t.Run(fmt.Sprintf("%dx%d", sz.cols, sz.rows), func(t *testing.T) {
			d, _ := yinYangAt(sz.rows, sz.cols)
			bands := uniformBands(0.5)
			gone, both := [2]int{}, [2]int{}
			maxGone := max(10, int(math.Ceil(1/(0.11*d.size))))
			for step := 0; d.angle < 6*math.Pi; step++ {
				yinYangHits(bands, 0, 3, 0.5, 0.95, step%16 == 0)
				yinYangHits(bands, 6, len(bands), 0.5, 0.9, step%16 == 8)
				d.advance(bands, true)
				for i := range d.koi {
					px := yinYangDrawAlone(d, i)
					left, right := false, false
					for y := range d.h {
						left = left || px[y*d.w] != yinYangWater
						right = right || px[(y+1)*d.w-1] != yinYangWater
					}
					if both[i]++; !left || !right {
						both[i] = 0
					}
					if gone[i]++; slices.ContainsFunc(px, func(p int8) bool { return p != yinYangWater }) {
						gone[i] = 0
					}
					if both[i] > 5 || gone[i] > maxGone {
						t.Fatalf("step %d: koi %d on both edges for %d steps, gone for %d", step, i, both[i], gone[i])
					}
				}
			}
		})
	}
}

func TestYinYangDrums(t *testing.T) {
	for _, tt := range []struct {
		name                string
		from, to            int
		low, high           float64
		stripFlicks, flicks [2]bool // red and pale koi, in the strip and round the circle
		lotus               bool
	}{
		{"kick", 0, 3, 0.5, 0.95, [2]bool{true, false}, [2]bool{false, false}, true},
		{"snare", 3, 6, 0.3, 0.8, [2]bool{false, false}, [2]bool{true, false}, false},
		{"hi-hat", 6, 10, 0.1, 0.6, [2]bool{false, true}, [2]bool{false, true}, false},
	} {
		for _, sz := range []struct{ rows, cols int }{{7, 160}, {30, 90}} {
			t.Run(fmt.Sprintf("%s/%dx%d", tt.name, sz.cols, sz.rows), func(t *testing.T) {
				d, _ := yinYangAt(sz.rows, sz.cols)
				bands := uniformBands(0.4)
				clearance, breath := math.Inf(1), 0.0
				flicked := [2]bool{}
				for step := range 200 {
					yinYangHits(bands, tt.from, tt.to, tt.low, tt.high, step%10 == 0)
					d.advance(bands, true)
					breath = max(breath, d.breath)
					for i, k := range d.koi {
						flicked[i] = flicked[i] || k.flick > 0
					}
					if d.strip {
						fin := d.koi[1].joints[yinYangJoints-1].x - 2*d.size
						clearance = min(clearance, fin-(d.koi[0].joints[0].x+yinYangNose*d.size))
					}
				}
				want, lotus := tt.flicks, tt.lotus
				if d.strip {
					want, lotus = tt.stripFlicks, false
				}
				if flicked != want {
					t.Errorf("flicked %v, want %v", flicked, want)
				}
				if (breath > 0.3) != lotus {
					t.Errorf("the lotus opened by %.2f, want it to open: %v", breath, lotus)
				}
				if clearance < 3 {
					t.Errorf("the red koi came within %.1f pixels of the pale one's tail", clearance)
				}
			})
		}
	}
}

// Hi-hats as fast as the detector allows flick a koi at most once per rest.
func TestYinYangFlicksRestBetweenHits(t *testing.T) {
	d, _ := yinYangAt(30, 90)
	bands := uniformBands(0.4)
	flicks := 0
	const steps = 200
	for step := range steps {
		yinYangHits(bands, 6, len(bands), 0.05, 0.9, step%(d.hat.gap+1) == 0)
		d.advance(bands, true)
		if d.koi[1].flick == yinYangFlickSteps-1 {
			flicks++
		}
	}
	if flicks == 0 || flicks > steps/yinYangRest+1 {
		t.Errorf("%d flicks in %d steps, want 1 to %d", flicks, steps, steps/yinYangRest+1)
	}
}

// A flick eases out and back without a jump, even when a hit retriggers it,
// bends further for a harder hit, alternates sides with its swirl on the
// same side, and shows in the drawn tail.
func TestYinYangFlick(t *testing.T) {
	k := yinYangKoi{flickDir: 1}
	k.flickTail(1)
	prev, peak := 0.0, 0.0
	for ; k.flick >= 0; k.flick-- {
		bend := k.tailBend()
		if math.Abs(bend-prev) > 0.25 {
			t.Fatalf("flick snapped from %.3f to %.3f", prev, bend)
		}
		peak, prev = max(peak, bend), bend
	}
	if peak < 0.25 || prev != 0 {
		t.Errorf("flick peaked at %.3f and ended at %.3f", peak, prev)
	}
	for left := 0; left <= yinYangFlickSteps; left++ {
		k := yinYangKoi{flick: left, flickPower: 1}
		before := k.tailBend()
		if k.flickTail(1); k.flick == 0 || math.Abs(k.tailBend()-before) > 1e-9 {
			t.Errorf("a hit with %d steps left moved the tail from %.3f to %.3f", left, before, k.tailBend())
		}
	}
	soft := yinYangKoi{flickPower: 0.7, flick: yinYangFlickSteps / 2}
	hard := yinYangKoi{flickPower: 1.4, flick: yinYangFlickSteps / 2}
	if hard.tailBend() <= soft.tailBend() {
		t.Errorf("a hard hit bends %.3f, a soft one %.3f", hard.tailBend(), soft.tailBend())
	}

	d, _ := yinYangAt(30, 90)
	koi := &d.koi[0]
	dir := koi.flickDir
	for range 4 {
		koi.flick = 0
		koi.flickTail(1)
		if koi.flickDir != -dir {
			t.Fatal("flicks do not alternate sides")
		}
		dir = koi.flickDir
		d.swirls = d.swirls[:0]
		d.advance(uniformBands(0.4), true)
		if len(d.swirls) != 1 {
			t.Fatalf("a flick left %d swirls", len(d.swirls))
		}
		tail, root := koi.joints[yinYangJoints-1], koi.joints[yinYangJoints-2]
		s := d.swirls[0]
		if side := (tail.x-root.x)*(s.y-tail.y) - (tail.y-root.y)*(s.x-tail.x); s.spin != dir || side*dir <= 0 {
			t.Errorf("a flick to side %v left a swirl on the other side", dir)
		}
	}

	for _, sz := range []struct{ rows, cols int }{{7, 160}, {20, 60}} {
		d, _ := yinYangAt(sz.rows, sz.cols)
		still := yinYangDrawAlone(d, 0)
		d.koi[0].flick, d.koi[0].flickPower = yinYangFlickSteps/2, 1
		if slices.Equal(yinYangDrawAlone(d, 0), still) {
			t.Errorf("%dx%d: a flick left the drawn tail where it was", sz.cols, sz.rows)
		}
	}
}

func TestYinYangPad(t *testing.T) {
	d, v := yinYangAt(20, 40)
	for j := range d.koi[0].joints {
		d.koi[0].joints[j] = yinYangPoint{d.cx - float64(j)*yinYangSeg*d.size, d.cy}
	}
	d.Render(v)
	if got := d.px[int(d.cy)*d.w+int(d.cx-d.pad*0.9)]; got != yinYangPad {
		t.Errorf("the pad's edge shows tag %d, want the pad", got)
	}
	d, v = yinYangAt(7, 120)
	d.Render(v)
	if slices.Contains(d.px, yinYangPad) {
		t.Error("the strip draws a lily pad")
	}
}

// Drawn into a padded canvas through a lap and resizes, no koi pixel lands
// outside the circular view.
func TestYinYangCircularKoiStayInsidePanel(t *testing.T) {
	for _, sz := range []struct{ rows, cols int }{{3, 13}, {20, 40}, {40, 120}, {20, 8}} {
		t.Run(fmt.Sprintf("%dx%d", sz.cols, sz.rows), func(t *testing.T) {
			d, _ := yinYangAt(sz.rows, sz.cols)
			const margin = 16
			w, h := d.w+2*margin, d.h+2*margin
			bands := uniformBands(0.5)
			for frame := 0; d.angle < 2*math.Pi; frame++ {
				if frame > 10000 {
					t.Fatal("the koi did not complete a lap")
				}
				padded := *d
				if frame%100 == 0 {
					padded.layout(d.w, d.h)
				}
				padded.w, padded.h, padded.px, padded.fine = w, h, make([]int8, w*h), nil
				for i := range padded.koi {
					for j := range padded.koi[i].joints {
						padded.koi[i].joints[j].x += margin
						padded.koi[i].joints[j].y += margin
					}
					padded.drawKoi(&padded.koi[i], yinYangPatterns[i])
				}
				for p, tag := range padded.px {
					x, y := p%w, p/w
					if tag != yinYangWater && (x < margin || x >= w-margin || y < margin || y >= h-margin) {
						t.Fatalf("frame %d: koi pixel (%d, %d) is outside the panel", frame, x-margin, y-margin)
					}
				}
				for i := range bands {
					bands[i] = 0.5 + 0.4*math.Sin(float64(frame)/10)
				}
				d.advance(bands, true)
			}
		})
	}
}

func TestYinYangPause(t *testing.T) {
	d, v := yinYangAt(20, 60)
	d.koi[0].flick, d.koi[0].flickPower = yinYangFlickSteps, 1
	v.Tick(VisTickContext{Playing: true})
	if len(d.swirls) == 0 {
		t.Fatal("a flick left no swirl")
	}
	d.loud, d.mood, d.bloom, d.breath = 0.6, 0.5, 0.7, 1
	head := d.koi[0].joints[0]
	ctx := VisTickContext{Now: time.Unix(1, 0), Paused: true}
	for range 40 {
		v.Tick(ctx)
		ctx.Now = ctx.Now.Add(TickSlow)
	}
	if d.koi[0].joints[0] != head {
		t.Error("a paused koi moved")
	}
	if d.loud != 0.6 || d.mood != 0.5 || d.bloom != 0.7 {
		t.Errorf("paused, the mood moved to %.2f, %.2f, %.2f", d.loud, d.mood, d.bloom)
	}
	if v.PausedDecayPending(ctx) {
		t.Errorf("not settled: %d swirls, breath %.3f", len(d.swirls), d.breath)
	}
}

func TestYinYangMotionTracksElapsedTime(t *testing.T) {
	run := func(interval time.Duration) *yinYangDriver {
		d, v := yinYangAt(7, 120)
		v.bands = uniformBands(0.6)
		start := time.Unix(1, 0)
		for elapsed := time.Duration(0); elapsed <= 2*time.Second; elapsed += interval {
			v.Tick(VisTickContext{Now: start.Add(elapsed), Playing: true})
		}
		return d
	}
	want := run(TickFast)
	for _, interval := range []time.Duration{TickAnim, 25 * time.Millisecond, 100 * time.Millisecond, TickSlow} {
		if got := run(interval); want.angle == 0 || got.angle != want.angle || !yinYangSameMotion(got.koi, want.koi) {
			t.Errorf("motion at %v differs from the %v cadence", interval, TickFast)
		}
	}
}

func TestYinYangClockBoundsCatchUp(t *testing.T) {
	for _, tt := range []struct {
		gap   time.Duration
		steps int
	}{{0, 0}, {150 * time.Millisecond, 3}, {time.Second, 4}} {
		d, v := yinYangAt(7, 120)
		ctx := VisTickContext{Now: time.Unix(1, 0), Playing: true}
		v.Tick(ctx)
		before := d.angle
		ctx.Now = ctx.Now.Add(TickFast)
		v.Tick(ctx)
		step := d.angle - before
		for _, gap := range []time.Duration{tt.gap, TickFast} {
			before = d.angle
			ctx.Now = ctx.Now.Add(gap)
			v.Tick(ctx)
			want := step
			if gap == tt.gap {
				want = float64(tt.steps) * step
			}
			if got := d.angle - before; math.Abs(got-want) > 1e-9 {
				t.Errorf("after a %v gap, a tick of %v moved %v, want %v", tt.gap, gap, got, want)
			}
		}
	}
}

func TestYinYangSuspendResetsClock(t *testing.T) {
	for _, name := range []string{"overlay", "hidden", "paused"} {
		d, v := yinYangAt(7, 120)
		ctx := VisTickContext{Now: time.Unix(1, 0), Playing: true}
		v.Tick(ctx)
		ctx.Now = ctx.Now.Add(TickFast + TickFast/2)
		v.Tick(ctx)
		before := d.koi
		switch name {
		case "overlay":
			v.Tick(VisTickContext{Now: ctx.Now, Playing: true, OverlayActive: true})
		case "hidden":
			v.Suspend()
		case "paused":
			v.Tick(VisTickContext{Now: ctx.Now, Paused: true})
		}
		ctx.Now = ctx.Now.Add(5 * time.Second)
		v.Tick(ctx)
		ctx.Now = ctx.Now.Add(TickFast / 2)
		v.Tick(ctx)
		if !yinYangSameMotion(d.koi, before) {
			t.Errorf("%s: resume advanced hidden time", name)
		}
		ctx.Now = ctx.Now.Add(TickFast / 2)
		v.Tick(ctx)
		if yinYangSameMotion(d.koi, before) {
			t.Errorf("%s: motion did not resume after a full frame", name)
		}
	}
}

func TestYinYangFastTicksMoveBetweenSteps(t *testing.T) {
	for _, tt := range []struct {
		interval time.Duration
		between  bool
	}{{TickAnim, true}, {TickFast, false}} {
		d, v := yinYangAt(7, 160)
		v.bands = uniformBands(0.6)
		now, moved := time.Unix(1, 0), false
		for range 60 {
			v.Tick(VisTickContext{Now: now, Playing: true})
			v.Render()
			k := d.koi[0]
			moved = moved || k.sprite.hx != int(math.Floor(k.joints[0].x))
			now = now.Add(tt.interval)
		}
		if moved != tt.between {
			t.Errorf("at %v, drawn between steps: %v, want %v", tt.interval, moved, tt.between)
		}
	}
}

func TestYinYangRenderFitsThePanel(t *testing.T) {
	for _, sz := range []struct{ rows, cols int }{{1, 40}, {3, 13}, {5, 80}, {7, 120}, {9, 48}, {40, 160}} {
		d, v := yinYangAt(sz.rows, sz.cols)
		bands := make([]float64, DefaultSpectrumBands)
		for step := range 40 {
			for i := range bands {
				bands[i] = 0.5 + 0.4*math.Sin(float64(step*3+i))
			}
			d.advance(bands, true)
		}
		lines := strings.Split(d.Render(v), "\n")
		if len(lines) != sz.rows {
			t.Errorf("%dx%d: %d lines, want %d", sz.cols, sz.rows, len(lines), sz.rows)
		}
		for i, line := range lines {
			if w := ansi.StringWidth(line); w > sz.cols {
				t.Errorf("%dx%d: line %d is %d wide", sz.cols, sz.rows, i, w)
			}
		}
	}
}

// Both halves of a cell follow a theme change, including back from RGB
// colours to the terminal palette.
func TestYinYangStylesCoverEveryPair(t *testing.T) {
	t.Cleanup(func() { ApplyThemeColors(theme.Default()) })
	for _, th := range []theme.Theme{theme.Default(), testTheme, theme.Default()} {
		ApplyThemeColors(th)
		p := PaletteFor(th)
		colors := [...]color.Color{nil, p.Dim, p.SpectrumLow, p.SpectrumHigh, p.Text, p.SpectrumMid}
		for fg := yinYangDim; fg < yinYangTagCount; fg++ {
			for bg := yinYangWater; bg < yinYangTagCount; bg++ {
				style := lipgloss.NewStyle().Foreground(colors[fg])
				if bg != yinYangWater {
					style = style.Background(colors[bg])
				}
				if got := yinYangANSI[fg][bg]; got.prefix+"▀"+got.suffix != style.Render("▀") {
					t.Errorf("theme %s, tags %d over %d: cached style does not match", th.Name, fg, bg)
				}
			}
		}
	}
}

// A strip koi redrawn a fraction of a pixel away keeps its exact shape, and
// between whole-pixel moves it is not redrawn at all.
func TestYinYangStripKoiHoldTheirShape(t *testing.T) {
	for _, rows := range []int{5, 7} {
		d, _ := yinYangAt(rows, 160)
		want := yinYangDrawAlone(d, 1)
		base := d.koi[1]
		for _, shift := range []float64{0.1, 0.35, 0.6, 0.9} {
			k := &d.koi[1]
			*k = base
			k.sprite = yinYangSprite{}
			fx, fy := k.joints[0].x-math.Floor(k.joints[0].x), k.joints[0].y-math.Floor(k.joints[0].y)
			for j := range k.joints {
				k.joints[j].x += shift * (1 - fx)
				k.joints[j].y += shift * (1 - fy)
			}
			if !slices.Equal(yinYangDrawAlone(d, 1), want) {
				t.Errorf("%d rows: moving %.2f of a pixel changed the koi", rows, shift)
			}
		}

		bands := uniformBands(0.5)
		var last []int8
		lx, ly, moves := 0, 0, 0
		for step := range 200 {
			d.advance(bands, true)
			k := d.koi[1]
			px := yinYangDrawAlone(d, 1)
			hx, hy := int(math.Floor(k.joints[0].x)), int(math.Floor(k.joints[0].y))
			if hx == lx && hy == ly && k.flick == 0 && last != nil && !slices.Equal(px, last) {
				t.Fatalf("%d rows, step %d: the koi changed without moving", rows, step)
			}
			if hx != lx || hy != ly {
				moves++
			}
			last, lx, ly = px, hx, hy
		}
		if moves < 40 {
			t.Errorf("%d rows: the koi moved a pixel only %d times in 200 steps", rows, moves)
		}
	}
}

// While a koi swims and flicks it stays one piece, with no holes but its
// eyes, and a strip koi's eyes sit level either side of its head.
func TestYinYangKoiHaveNoHoles(t *testing.T) {
	for _, rows := range []int{5, 7, 30} {
		d, _ := yinYangAt(rows, 160)
		bands := uniformBands(0.5)
		for step := range 300 {
			if step%25 == 0 {
				d.koi[0].flickTail(1)
				d.koi[1].flickTail(1)
			}
			d.advance(bands, true)
			for i := range d.koi {
				px := yinYangDrawAlone(d, i)
				// A koi cut by the panel's edge may show in more than one piece.
				edge := false
				for p, tag := range px {
					x, y := p%d.w, p/d.w
					edge = edge || tag != yinYangWater && (x == 0 || y == 0 || x == d.w-1 || y == d.h-1)
				}
				if pieces, holes := yinYangRegions(px, d.w); pieces > 1 && !edge || holes > 2 {
					t.Fatalf("%d rows, step %d: koi %d is in %d pieces with %d holes", rows, step, i, pieces, holes)
				}
				hx, hy := int(math.Floor(d.koi[i].joints[0].x)), int(math.Floor(d.koi[i].joints[0].y))
				reach := int(math.Ceil(yinYangHalfBody * d.size))
				ex := ((hx+1)%int(d.lap) + int(d.lap)) % int(d.lap)
				if !d.strip || rows < 7 || ex >= d.w || hy-reach < 0 || hy+reach >= d.h {
					continue
				}
				water := func(y int) bool { return px[y*d.w+ex] == yinYangWater }
				eye := 1
				for eye < reach && !(water(hy-eye) && water(hy+eye)) {
					eye++
				}
				if eye == reach || water(hy-eye-1) || water(hy+eye+1) {
					t.Fatalf("%d rows, step %d: koi %d has no level pair of eyes", rows, step, i)
				}
			}
		}
	}
}

// yinYangRegions counts a drawing's pieces, joined at sides or corners, and
// the water pixels it closes in.
func yinYangRegions(px []int8, w int) (pieces, holes int) {
	h := len(px) / w
	seen := make([]bool, len(px))
	flood := func(start int, koi bool) {
		stack := []int{start}
		seen[start] = true
		for len(stack) > 0 {
			p := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			for dy := -1; dy <= 1; dy++ {
				for dx := -1; dx <= 1; dx++ {
					x, y := p%w+dx, p/w+dy
					if x < 0 || y < 0 || x >= w || y >= h || !koi && dx != 0 && dy != 0 {
						continue
					}
					if q := y*w + x; !seen[q] && (px[q] != yinYangWater) == koi {
						seen[q] = true
						stack = append(stack, q)
					}
				}
			}
		}
	}
	for p, tag := range px {
		if x, y := p%w, p/w; tag == yinYangWater && !seen[p] && (x == 0 || y == 0 || x == w-1 || y == h-1) {
			flood(p, false)
		}
	}
	for p, tag := range px {
		switch {
		case seen[p]:
		case tag == yinYangWater:
			holes++
		default:
			pieces++
			flood(p, true)
		}
	}
	return pieces, holes
}

// Round the circle a koi sways its rear half, wider with a wider stroke; a
// strip koi does not sway.
func TestYinYangSwayFollowsTheStroke(t *testing.T) {
	for _, tt := range []struct {
		rows, cols int
		sways      bool
	}{{30, 90, true}, {7, 160, false}} {
		d, _ := yinYangAt(tt.rows, tt.cols)
		d.koi[1].phase = 1
		draw := func(stroke float64) []int8 {
			d.koi[1].stroke, d.koi[1].sprite = stroke, yinYangSprite{}
			return yinYangDrawAlone(d, 1)
		}
		diff := func(a, b []int8) (n int) {
			for i := range a {
				if a[i] != b[i] {
					n++
				}
			}
			return n
		}
		still, gentle, wide := draw(0), draw(0.4), draw(1.2)
		if g, w := diff(still, gentle), diff(still, wide); tt.sways && (g == 0 || w <= g) || !tt.sways && w != 0 {
			t.Errorf("%dx%d: sway moved %d pixels at a gentle stroke and %d at a wide one", tt.cols, tt.rows, g, w)
		}
	}
}

// The stroke and the pace round the circle follow the song's mood: a sudden
// change in the music moves them little in a second and most of the way in
// ten.
func TestYinYangMoodChangesSlowly(t *testing.T) {
	d, _ := yinYangAt(30, 90)
	d.loud, d.mood = 0, 0
	for i := range d.koi {
		d.koi[i].stroke = 0.3
	}
	bands := make([]float64, DefaultSpectrumBands)
	after := func(steps int) (stroke, pace float64) {
		for step := range steps {
			for i := range bands {
				bands[i] = 0.2 + 0.8*float64(step%2)
			}
			before := d.angle
			d.advance(bands, true)
			pace = d.angle - before
		}
		return d.koi[0].stroke, pace
	}
	_, calm := after(1)
	stroke1, pace1 := after(int(time.Second / yinYangStep))
	stroke10, pace10 := after(int(9 * time.Second / yinYangStep))
	if stroke1-0.3 > (stroke10-0.3)*0.4 || pace1-calm > (pace10-calm)*0.4 {
		t.Errorf("after a second, stroke %.2f and pace %.5f; after ten, %.2f and %.5f", stroke1, pace1, stroke10, pace10)
	}
	if stroke10 <= 0.5 || pace10 <= calm*1.2 {
		t.Errorf("intense music left the stroke at %.2f and the pace at %.5f from %.5f", stroke10, pace10, calm)
	}
}

// The lotus opens with each kick and closes before the next, lets go of a
// bass that holds, opens its white petals most, and blooms with the mood.
func TestYinYangLotusFollowsTheBass(t *testing.T) {
	d, v := yinYangAt(30, 90)
	bands := uniformBands(0.4)
	var breaths []float64
	for step := range 200 {
		yinYangHits(bands, 0, 2, 0.5, 0.95, step < 120 && step%10 == 0)
		if step >= 120 {
			yinYangHits(bands, 0, 2, 0.9, 0.9, false)
		}
		d.advance(bands, true)
		breaths = append(breaths, d.breath)
	}
	for kick := 50; kick < 120; kick += 10 {
		if breaths[kick] < 0.8 || breaths[kick+7] > 0.3 {
			t.Errorf("kick at step %d: open by %.2f, seven steps on %.2f", kick, breaths[kick], breaths[kick+7])
		}
	}
	if held := breaths[len(breaths)-1]; held > 0.05 {
		t.Errorf("a held bass kept the lotus open by %.2f", held)
	}

	d.breath = 0
	shutOuter, shutInner := d.lotusReach()
	d.breath = 1
	openOuter, openInner := d.lotusReach()
	if openInner <= shutInner || openOuter-shutOuter <= openInner-shutInner {
		t.Errorf("the bass moved the white petals %.2f and the red %.2f", openOuter-shutOuter, openInner-shutInner)
	}
	draw := func(outer, inner float64) []int8 {
		d.Render(v)
		clear(d.px)
		d.drawLotus(outer, inner)
		return slices.Clone(d.px)
	}
	shut, open := draw(shutOuter, shutInner), draw(openOuter, openInner)
	if yinYangCount(open, yinYangInk) < yinYangCount(shut, yinYangInk)*3/2 || yinYangCount(open, yinYangRed) <= yinYangCount(shut, yinYangRed) {
		t.Error("an open lotus is not drawn larger")
	}

	for range 40 {
		d.mood = 0.9
		d.advance(bands, true)
	}
	if d.bloom < 0.45 || d.bloom > 0.9 {
		t.Errorf("two seconds of an intense mood bloomed the lotus to %.2f", d.bloom)
	}
}

func TestYinYangBassHistoryFollowsPlayback(t *testing.T) {
	for _, tt := range []struct {
		name    string
		strip   bool
		playing bool
		level   float64
		steps   int
		want    float64
	}{
		{"silence before expiry", false, true, 0, 38, 0.5 / 0.9},
		{"silence after expiry", false, true, 0, 39, 1},
		{"quiet playback", false, true, 0.01, 39, 1},
		{"strip before expiry", true, true, 0.2, 38, 0.3 / 0.7},
		{"strip after expiry", true, true, 0.2, 39, 1},
		{"held bass in strip", true, true, 0.5, 39, 0},
		{"paused", false, false, 0, 100, 0.2},
	} {
		t.Run(tt.name, func(t *testing.T) {
			d, _ := yinYangAt(30, 90)
			for range 40 {
				d.advance(uniformBands(0.4), true)
			}
			d.advance(uniformBands(0.9), true)
			breath := d.breath
			if tt.strip {
				d.layout(160, 14)
			}
			for range tt.steps {
				d.advance(uniformBands(tt.level), tt.playing)
			}
			if want := breath * math.Pow(0.8, float64(tt.steps)); math.Abs(d.breath-want) > 1e-9 {
				t.Fatalf("inactive lotus breath = %.3f, want decay to %.3f", d.breath, want)
			}
			if tt.strip {
				d.layout(90, 60)
			}
			d.advance(uniformBands(0.5), true)
			if math.Abs(d.breath-tt.want) > 1e-9 {
				t.Errorf("returning lotus breath = %.3f, want %.3f", d.breath, tt.want)
			}
		})
	}
}

// BenchmarkYinYangFrame measures a step of motion and a render. A strip koi
// is redrawn only when it moves a pixel; "redraw" forces it every frame.
func BenchmarkYinYangFrame(b *testing.B) {
	for _, bc := range []struct {
		name       string
		rows, cols int
		redraw     bool
	}{{"strip", 7, 160, false}, {"strip/redraw", 7, 160, true}, {"circle", 40, 160, false}} {
		b.Run(bc.name, func(b *testing.B) {
			d, v := yinYangAt(bc.rows, bc.cols)
			bands := uniformBands(0.5)
			b.ReportAllocs()
			for b.Loop() {
				d.advance(bands, true)
				if bc.redraw {
					d.koi[0].sprite.px, d.koi[1].sprite.px = nil, nil
				}
				_ = d.Render(v)
			}
		})
	}
}
