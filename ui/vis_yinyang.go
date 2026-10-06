package ui

import (
	"image/color"
	"math"
	"sort"
	"strings"
	"time"

	"charm.land/lipgloss/v2"
)

// YinYang draws two koi circling a lily pad, or following an S-curve in a
// short, wide panel. Each koi is a chain of joints, drawn in sextant pixels.

const (
	yinYangJoints     = 13
	yinYangSeg        = 1.5 // joint spacing; a koi is 18 units long
	yinYangFlickSteps = 8
	yinYangMaxPower   = 1.4
	yinYangSwirlLife  = 12
	yinYangStandout   = 1.5 // a hit flicks a tail only this far past its threshold
	yinYangRest       = 20  // steps between one koi's flicks
	yinYangBassSteps  = 40  // the lotus judges the bass against this many steps
	yinYangStep       = TickFast
	yinYangMaxSteps   = 4
	yinYangQuiet      = 0.02
	yinYangStripPace  = 2.5
	yinYangArcSteps   = 64
	yinYangGap        = 36.0 // strip: the red koi's distance behind the pale one
	yinYangHalfBody   = 6.3  // half-height of a koi with its fins spread
	yinYangNose       = 2.6
	yinYangTailFin    = 1.0
)

// yinYangRadii is a koi's half-width at each joint, head to tail.
var yinYangRadii = [yinYangJoints]float64{2.5, 2.9, 2.9, 2.7, 2.4, 2.05, 1.75, 1.45, 1.2, 1.0, 0.85, 0.7, 0.6}

const (
	yinYangWater int8 = iota
	yinYangDim
	yinYangPad
	yinYangRed
	yinYangInk
	yinYangGold
	yinYangTagCount
)

type yinYangPattern struct {
	body int8
	back [yinYangJoints]int8
}

var yinYangPatterns = [2]yinYangPattern{
	{body: yinYangRed, back: [yinYangJoints]int8{0, yinYangInk, yinYangInk, 0, yinYangInk, yinYangInk, 0, 0, yinYangInk, yinYangInk, 0, yinYangInk, 0}},
	{body: yinYangInk, back: [yinYangJoints]int8{0, yinYangRed, yinYangRed, 0, 0, yinYangRed, yinYangRed, yinYangRed, 0, 0, 0, 0, 0}},
}

type yinYangPoint struct{ x, y float64 }

type yinYangKoi struct {
	joints, prevJoints [yinYangJoints]yinYangPoint
	offset             float64
	phase, phaseStep   float64
	stroke             float64
	flick, flickRest   int
	flickPower         float64
	flickDir           float64
	sprite             yinYangSprite
}

// yinYangSprite is a strip koi as last drawn with its head at pixel (hx, hy).
type yinYangSprite struct {
	px             []int8
	w              int
	hx, hy, dx, dy int
	flicked        bool
}

func (k *yinYangKoi) flickTail(power float64) {
	// A hit during a flick retraces its outward half at the same strength,
	// so the tail never snaps.
	if k.flick == 0 {
		k.flickPower, k.flickDir = power, -k.flickDir
	}
	k.flick = max(k.flick, yinYangFlickSteps-k.flick)
}

func (k *yinYangKoi) tailBend() float64 {
	wave := math.Sin(math.Pi * float64(k.flick) / yinYangFlickSteps)
	return 0.6 * wave * wave * k.flickPower
}

type yinYangSwirl struct {
	x, y, angle, spin float64
	life              int
}

// yinYangOnset finds a sudden rise across a range of bands, judged against
// how much those bands usually rise in this song.
type yinYangOnset struct {
	from, to   float64
	k, floor   float64
	gap, since int
	mean, dev  float64
}

func (o *yinYangOnset) reset() {
	o.mean, o.dev, o.since = 0.03, 0.02, o.gap
}

// step reports a hit and its strength, 1 at the threshold.
func (o *yinYangOnset) step(bands, prev []float64) (bool, float64) {
	n := len(bands)
	flux := 0.0
	for i := int(o.from * float64(n)); i < int(o.to*float64(n)) && i < len(prev); i++ {
		flux += max(0, bands[i]-prev[i])
	}
	threshold := max(o.mean+o.k*o.dev, o.floor)
	hit := flux > threshold && o.since >= o.gap
	o.mean = o.mean*0.92 + flux*0.08
	o.dev = o.dev*0.92 + math.Abs(flux-o.mean)*0.08
	if hit {
		o.since = 0
	} else {
		o.since++
	}
	return hit, flux / threshold
}

type yinYangDriver struct {
	spectrumDriverBase

	w, h                 int
	strip                bool
	cx, cy, radius       float64
	size, pad            float64
	lap, trackLen, waves float64
	waveArc              []float64

	// loud follows the music's intensity over about a second, mood over
	// about five.
	angle, loud, mood   float64
	bloom, breath       float64
	bassLevels          [yinYangBassSteps]float64
	bassNext, bassCount int
	lastTick            time.Time
	pending             time.Duration

	lo, hi, prev     []float64
	kick, snare, hat yinYangOnset

	koi    [2]yinYangKoi
	swirls []yinYangSwirl
	px     []int8
	fine   []int8
}

func newYinYangDriver() visModeDriver {
	d := &yinYangDriver{
		kick:  yinYangOnset{from: 0, to: 0.3, k: 2.0, floor: 0.05, gap: 6},
		snare: yinYangOnset{from: 0.3, to: 0.6, k: 2.0, floor: 0.04, gap: 5},
		hat:   yinYangOnset{from: 0.6, to: 1, k: 1.8, floor: 0.025, gap: 4},
	}
	d.OnEnter(nil)
	return d
}

func (d *yinYangDriver) OnEnter(*Visualizer) {
	d.w, d.h = 0, 0
	d.angle, d.loud, d.mood = 0, 0.3, 0.3
	d.bloom, d.breath = 0.4, 0
	d.bassNext, d.bassCount = 0, 0
	d.lastTick, d.pending = time.Time{}, 0
	d.lo, d.hi, d.prev = nil, nil, nil
	d.kick.reset()
	d.snare.reset()
	d.hat.reset()
	d.swirls = nil
}

func (d *yinYangDriver) pauseSettled() bool {
	return len(d.swirls) == 0 && d.koi[0].flick == 0 && d.koi[1].flick == 0 && d.breath < 0.01
}

func (d *yinYangDriver) layout(w, h int) {
	d.w, d.h = w, h
	wd, hd := float64(w), float64(h)
	d.cx, d.cy = wd/2, hd/2
	d.strip = wd > hd*3
	if d.strip {
		// The koi may swing partly out of view at the top and bottom of the
		// wave. Past the right edge is hidden water one koi long, so a koi
		// leaves whole and comes straight back in on the left.
		d.size = min(hd*0.84/(2*yinYangHalfBody), wd/80)
		d.radius = min(hd*0.3, d.size*4.5)
		hidden := yinYangSeg*(yinYangJoints-1) + yinYangNose + yinYangTailFin
		d.lap = math.Round(wd + hidden*d.size)
		d.waves = max(1, math.Round(d.lap/(55*d.size)))
		d.pad = 0
		// Measure one wave, so the koi keep their speed where it runs steep.
		wave := d.lap / d.waves
		d.waveArc = append(d.waveArc[:0], 0)
		for i := 1; i <= yinYangArcSteps; i++ {
			x0, x1 := float64(i-1)*wave/yinYangArcSteps, float64(i)*wave/yinYangArcSteps
			dy := (math.Cos(2*math.Pi*x1/wave) - math.Cos(2*math.Pi*x0/wave)) * d.radius
			d.waveArc = append(d.waveArc, d.waveArc[i-1]+math.Hypot(x1-x0, dy))
		}
		d.trackLen = d.waves * d.waveArc[yinYangArcSteps]
	} else {
		m := min(wd, hd, 160)
		d.radius = m * 0.36
		d.lap = 2 * math.Pi * d.radius
		d.trackLen = d.lap
		d.pad = m * 0.17
		d.size = min(m*0.34*1.35/18, (wd/2-d.radius)/yinYangHalfBody, (hd/2-d.radius)/yinYangHalfBody)
	}
	d.koi[0] = yinYangKoi{stroke: 0.6, flickDir: 1}
	d.koi[1] = yinYangKoi{offset: math.Pi, phase: 1.5, stroke: 0.6, flickDir: 1}
	if d.strip {
		d.koi[0].offset, d.koi[1].offset = -d.toAngle(yinYangGap), 0
	}
	for i := range d.koi {
		d.lineUp(&d.koi[i])
		d.koi[i].prevJoints = d.koi[i].joints
	}
	d.swirls = d.swirls[:0]
}

// toAngle turns a distance in koi units into an angle along the track.
func (d *yinYangDriver) toAngle(dist float64) float64 {
	return dist * d.size * 2 * math.Pi / d.trackLen
}

func (d *yinYangDriver) lineUp(k *yinYangKoi) {
	a := d.angle + k.offset
	for j := range k.joints {
		k.joints[j] = d.trackPoint(a)
		a -= d.toAngle(yinYangSeg)
	}
	d.follow(k)
}

// trackPoint keeps strip x continuous across laps; only drawing wraps, so a
// koi crossing the edge never pulls its tail back.
func (d *yinYangDriver) trackPoint(a float64) yinYangPoint {
	if !d.strip {
		return yinYangPoint{d.cx + math.Cos(a)*d.radius, d.cy + math.Sin(a)*d.radius}
	}
	per := d.waveArc[yinYangArcSteps]
	s := a / (2 * math.Pi) * d.trackLen
	n := math.Floor(s / per)
	s -= n * per
	i := max(1, min(yinYangArcSteps, sort.SearchFloat64s(d.waveArc, s)))
	t := (s - d.waveArc[i-1]) / (d.waveArc[i] - d.waveArc[i-1])
	wave := d.lap / d.waves
	x := (float64(i-1) + t) * wave / yinYangArcSteps
	return yinYangPoint{n*wave + x + float64(d.w)*0.6, d.cy + math.Cos(2*math.Pi*x/wave)*d.radius}
}

func (d *yinYangDriver) Tick(v *Visualizer, ctx VisTickContext) {
	defaultDriverTick(v, ctx, d.AnalysisSpec(v))
	if ctx.OverlayActive {
		d.lastTick, d.pending = time.Time{}, 0
		return
	}
	w, h := v.columns()*2, v.Rows*4
	if w <= 0 || h <= 0 {
		return
	}
	if w != d.w || h != d.h {
		d.layout(w, h)
	}
	// Fixed steps keep the pace the same at any tick rate.
	steps := 1
	if !ctx.Now.IsZero() {
		if !d.lastTick.IsZero() {
			d.pending += ctx.Now.Sub(d.lastTick)
		}
		d.lastTick = ctx.Now
		steps = int(d.pending / yinYangStep)
		d.pending -= time.Duration(steps) * yinYangStep
		if steps > yinYangMaxSteps {
			steps, d.pending = yinYangMaxSteps, 0
		}
	}
	bands := v.SmoothedBands()
	for range steps {
		d.advance(bands, ctx.Playing)
	}
}

func (d *yinYangDriver) advance(bands []float64, playing bool) {
	n := len(bands)
	if len(d.lo) != n {
		d.lo, d.hi, d.prev = make([]float64, n), make([]float64, n), nil
		for i := range d.lo {
			d.lo[i] = 1
		}
	}
	// Each band against its own recent range: mastered music keeps the bass
	// near the top and the treble near the floor.
	energy, avg := 0.0, 0.0
	for i, b := range bands {
		energy += b
		d.hi[i] = max(b, d.hi[i]-0.004)
		d.lo[i] = min(b, d.lo[i]+0.004)
		avg += min(1, max(0, (b-d.lo[i])/max(d.hi[i]-d.lo[i], 0.12)))
	}
	if n > 0 {
		energy /= float64(n)
		avg /= float64(n)
	}
	quiet := !playing || energy < yinYangQuiet
	if playing {
		d.loud += (avg - d.loud) * 0.05
		d.mood += (avg - d.mood) * 0.01
		bloom := 0.0
		if !quiet {
			bloom = min(1, max(0, (d.mood-0.1)/0.5))
		}
		d.bloom += (bloom - d.bloom) * 0.02
	}

	// The hi-hat flicks the pale koi's tail. Round the circle the snare
	// flicks the red koi's and the lotus shows the kick; in the strip the
	// kick flicks the red koi's.
	if d.prev != nil {
		for _, drum := range [3]struct {
			onset *yinYangOnset
			koi   int
			here  bool
		}{{&d.kick, 0, d.strip}, {&d.snare, 0, !d.strip}, {&d.hat, 1, true}} {
			hit, strength := drum.onset.step(bands, d.prev)
			k := &d.koi[drum.koi]
			if hit && drum.here && !quiet && strength >= yinYangStandout && k.flickRest == 0 {
				k.flickTail(min(yinYangMaxPower, 0.6+0.3*strength))
				k.flickRest = yinYangRest
			}
		}
	}
	d.prev = append(d.prev[:0], bands...)

	// The lotus rises and falls with the bass like a bar, judged against the
	// bass's own range over the last two seconds so it never sticks open.
	d.breath *= 0.8
	// Keep the history current through silence and strip playback too.
	if playing && n > 0 {
		level := 0.0
		low := max(1, n/5)
		for _, b := range bands[:low] {
			level += b / float64(low)
		}
		d.bassLevels[d.bassNext] = level
		d.bassNext = (d.bassNext + 1) % yinYangBassSteps
		d.bassCount = min(d.bassCount+1, yinYangBassSteps)
		if !quiet && !d.strip {
			lo, hi := level, level
			for _, b := range d.bassLevels[:d.bassCount] {
				lo, hi = min(lo, b), max(hi, b)
			}
			d.breath = (level - lo) / max(hi-lo, 0.08)
		}
	}

	pace := 0.0
	if playing {
		switch {
		case quiet:
			pace = 0.066
		case d.strip:
			pace = 0.11 + 0.22*d.loud
		default:
			pace = 0.09 + 0.13*d.mood
		}
		if d.strip {
			// A small strip koi needs about a pixel a frame to move smoothly.
			least := 0.7
			if !quiet {
				least = 1.0 + 1.0*d.loud
			}
			pace = max(pace*yinYangStripPace, least/d.size)
		}
		d.angle += d.toAngle(pace)
	}
	stroke, beat := 0.1, 0.09
	if !quiet {
		stroke, beat = 0.3+d.mood, 0.125+0.085*d.mood
	}
	for i := range d.koi {
		k := &d.koi[i]
		k.prevJoints, k.phaseStep = k.joints, 0
		k.flickRest = max(0, k.flickRest-1)
		k.stroke += (stroke - k.stroke) * 0.02
		if playing {
			k.phaseStep = beat
			k.phase += beat
			k.joints[0] = d.trackPoint(d.angle + k.offset)
			d.follow(k)
		}
		if k.flick == yinYangFlickSteps && !d.strip {
			tail, root := k.joints[yinYangJoints-1], k.joints[yinYangJoints-2]
			out := math.Atan2(tail.y-root.y, tail.x-root.x) + k.flickDir*math.Pi/2
			x, y := tail.x+math.Cos(out)*1.2*d.size, tail.y+math.Sin(out)*1.2*d.size
			d.swirls = append(d.swirls, yinYangSwirl{x: x, y: y, angle: out, spin: k.flickDir, life: yinYangSwirlLife})
		}
		if k.flick > 0 {
			k.flick--
		}
	}
	live := d.swirls[:0]
	for _, s := range d.swirls {
		if s.life--; s.life > 0 {
			live = append(live, s)
		}
	}
	d.swirls = live
}

func (d *yinYangDriver) follow(k *yinYangKoi) {
	seg := yinYangSeg * d.size
	for j := 1; j < yinYangJoints; j++ {
		a, b := k.joints[j-1], &k.joints[j]
		dx, dy := b.x-a.x, b.y-a.y
		if dist := math.Hypot(dx, dy); dist > 0 {
			b.x, b.y = a.x+dx/dist*seg, a.y+dy/dist*seg
		}
	}
}

func (d *yinYangDriver) Render(v *Visualizer) string {
	rows, cols := v.Rows, v.columns()
	if rows <= 0 || cols <= 0 {
		return strings.Repeat("\n", max(0, rows-1))
	}
	w, h := cols*2, rows*4
	if w != d.w || h != d.h {
		d.layout(w, h)
	}
	if cap(d.px) < w*h {
		d.px = make([]int8, w*h)
	}
	d.px = d.px[:w*h]
	clear(d.px)

	for _, s := range d.swirls {
		d.drawSwirl(s)
	}
	// Between steps, as at 60 FPS, carry each koi on by the part of the next
	// step that has passed.
	ahead := 0.0
	if !d.lastTick.IsZero() {
		ahead = min(1, float64(d.pending)/float64(yinYangStep))
	}
	for i := range d.koi {
		k := d.koi[i]
		for j, p := range k.joints {
			k.joints[j].x += (p.x - k.prevJoints[j].x) * ahead
			k.joints[j].y += (p.y - k.prevJoints[j].y) * ahead
		}
		k.phase += k.phaseStep * ahead
		d.drawKoi(&k, yinYangPatterns[i])
		d.koi[i].sprite = k.sprite
	}
	// The pad floats over the koi.
	notchX, notchY, notchTan := math.Cos(0.9), math.Sin(0.9), math.Tan(0.28)
	d.disc(d.cx, d.cy, d.pad, func(dx, dy float64) int8 {
		ahead, aside := dx*notchX+dy*notchY, dx*notchY-dy*notchX
		if ahead > 0 && math.Abs(aside) < notchTan*ahead && dx*dx+dy*dy > 0.04 {
			return -1
		}
		return yinYangPad
	})
	d.drawLotus(d.lotusReach())
	return d.encode(rows, cols)
}

// lotusReach is how far the outer and inner petals reach towards the pad's
// edge: the mood opens the flower slowly, the bass on every kick.
func (d *yinYangDriver) lotusReach() (outer, inner float64) {
	return 0.5 + 0.12*d.bloom + 0.2*d.breath, 0.3 + 0.08*d.bloom + 0.08*d.breath
}

// yinYangPetal is a petal's half-width along it, broad at its base and
// pointed at its tip, sampled once since the lotus is drawn every frame.
var yinYangPetal = func() (w [65]float64) {
	for i := range w {
		w[i] = 0.36 * math.Pow(math.Sin(math.Pi*math.Pow(float64(i)/64, 0.8)), 0.6)
	}
	return w
}()

func yinYangPetalWidth(along float64) float64 {
	f := along * 64
	i := int(f)
	return yinYangPetal[i] + (yinYangPetal[i+1]-yinYangPetal[i])*(f-float64(i))
}

func (d *yinYangDriver) drawLotus(outer, inner float64) {
	const petals = 6
	for _, ring := range [2]struct {
		length, turn float64
		tag          int8
	}{{d.pad * outer, 0, yinYangInk}, {d.pad * inner, math.Pi / petals, yinYangRed}} {
		d.disc(d.cx, d.cy, ring.length, func(dx, dy float64) int8 {
			a := math.Atan2(dy, dx) - ring.turn
			a -= math.Round(a/(2*math.Pi/petals)) * 2 * math.Pi / petals
			r := math.Hypot(dx, dy)
			along, across := r*math.Cos(a), r*math.Sin(a)
			if along > 0 && along < 1 && math.Abs(across) <= yinYangPetalWidth(along) {
				return ring.tag
			}
			return -1
		})
	}
	d.fill(d.cx, d.cy, d.pad*inner*0.35, yinYangGold)
}

// set writes tag t at (x, y), wrapping round the strip lap and dropping
// pixels in the hidden water. A negative tag leaves the pixel as it is.
func (d *yinYangDriver) set(x, y int, t int8) {
	if d.strip {
		lap := int(d.lap)
		x %= lap
		if x < 0 {
			x += lap
		}
	}
	if t < 0 || x < 0 || y < 0 || x >= d.w || y >= d.h {
		return
	}
	d.px[y*d.w+x] = t
}

func (d *yinYangDriver) plot(x, y float64, t int8) {
	d.set(int(math.Floor(x)), int(math.Floor(y)), t)
}

// disc fills a disc of radius r, taking each pixel's tag from shade at its
// offset from the centre, in radii.
func (d *yinYangDriver) disc(cx, cy, r float64, shade func(dx, dy float64) int8) {
	if r <= 0 {
		return
	}
	for y := max(0, int(math.Floor(cy-r))); y <= min(d.h-1, int(math.Ceil(cy+r))); y++ {
		for x := max(0, int(math.Floor(cx-r))); x <= min(d.w-1, int(math.Ceil(cx+r))); x++ {
			dx, dy := (float64(x)+0.5-cx)/r, (float64(y)+0.5-cy)/r
			if dx*dx+dy*dy <= 1 {
				d.set(x, y, shade(dx, dy))
			}
		}
	}
}

func (d *yinYangDriver) fill(cx, cy, r float64, t int8) {
	d.disc(cx, cy, r, func(float64, float64) int8 { return t })
}

func (d *yinYangDriver) drawSwirl(s yinYangSwirl) {
	open := float64(yinYangSwirlLife-s.life) / yinYangSwirlLife
	r := (1 + 2.5*open) * d.size
	const points = 14
	for i := 0; i <= points; i++ {
		t := float64(i) / points
		if t >= 1-open*0.6 {
			break
		}
		a := s.angle + t*4.5*s.spin
		rr := r * (0.4 + 0.6*t)
		d.plot(s.x+math.Cos(a)*rr, s.y+math.Sin(a)*rr, yinYangDim)
	}
}

// drawKoi draws a koi round the circle straight onto the pixels, so its
// edges move a pixel at a time. A strip koi is small enough that its outline
// would shimmer as it bends, so it is redrawn only when it moves a whole
// pixel or flicks, and held still in between.
func (d *yinYangDriver) drawKoi(k *yinYangKoi, pat yinYangPattern) {
	if d.strip {
		head := k.joints[0]
		sp := &k.sprite
		if hx, hy := int(math.Floor(head.x)), int(math.Floor(head.y)); sp.px == nil || hx != sp.hx || hy != sp.hy || k.flick > 0 || sp.flicked {
			d.drawSprite(k, pat)
		}
		for i, t := range sp.px {
			if t != yinYangWater {
				d.set(sp.hx+sp.dx+i%sp.w, sp.hy+sp.dy+i/sp.w, t)
			}
		}
		return
	}
	// Drawn alone in a box around it first, so closing its gaps cannot join
	// it to the other koi.
	reach := math.Ceil(yinYangHalfBody*d.size) + 2
	x0, y0, x1, y1 := k.joints[0].x, k.joints[0].y, k.joints[0].x, k.joints[0].y
	for _, p := range k.joints {
		x0, x1 = min(x0, p.x), max(x1, p.x)
		y0, y1 = min(y0, p.y), max(y1, p.y)
	}
	ox, oy := math.Floor(x0)-reach, math.Floor(y0)-reach
	bw, bh := int(math.Ceil(x1)+reach-ox)+1, int(math.Ceil(y1)+reach-oy)+1
	if cap(d.fine) < bw*bh {
		d.fine = make([]int8, bw*bh)
	}
	alone := yinYangDriver{w: bw, h: bh, size: d.size, px: d.fine[:bw*bh]}
	clear(alone.px)
	local := *k
	for i, p := range k.joints {
		local.joints[i] = yinYangPoint{p.x - ox, p.y - oy}
	}
	alone.drawShape(&local, pat, true)
	yinYangCloseGaps(alone.px, bw, pat.body)
	for i, t := range alone.px {
		if t != yinYangWater {
			d.set(int(ox)+i%bw, int(oy)+i/bw, t)
		}
	}
	j := k.joints
	head := math.Atan2(j[0].y-j[2].y, j[0].x-j[2].x)
	r := yinYangRadii[0] * d.size * 0.75
	for _, side := range [2]float64{-1, 1} {
		a := head + side*1.1
		d.plot(j[0].x+math.Cos(a)*r, j[0].y+math.Sin(a)*r, yinYangWater)
	}
}

// drawSprite draws a strip koi into its sprite, from the middle of its head's
// pixel so it keeps its shape as it moves.
func (d *yinYangDriver) drawSprite(k *yinYangKoi, pat yinYangPattern) {
	head := k.joints[0]
	hx, hy := math.Floor(head.x), math.Floor(head.y)
	sx, sy := hx+0.5-head.x, hy+0.5-head.y
	reach := math.Ceil(yinYangHalfBody*d.size) + 1
	x0, y0, x1, y1 := hx, hy, hx, hy
	for _, p := range k.joints {
		x0, x1 = min(x0, math.Floor(p.x+sx)), max(x1, math.Floor(p.x+sx))
		y0, y1 = min(y0, math.Floor(p.y+sy)), max(y1, math.Floor(p.y+sy))
	}
	ox, oy := x0-reach, y0-reach
	bw, bh := int(x1-x0+2*reach)+1, int(y1-y0+2*reach)+1

	local := *k
	for i, p := range k.joints {
		local.joints[i] = yinYangPoint{p.x + sx - ox, p.y + sy - oy}
	}
	sprite := yinYangDriver{w: bw, h: bh, size: d.size, px: make([]int8, bw*bh)}
	sprite.drawShape(&local, pat, false)
	yinYangCloseGaps(sprite.px, bw, pat.body)
	addStripEyes(sprite.px, bw, int(hx-ox), int(hy-oy), pat.body)
	k.sprite = yinYangSprite{px: sprite.px, w: bw, hx: int(hx), hy: int(hy), dx: int(ox - hx), dy: int(oy - hy), flicked: k.flick > 0}
}

// yinYangCloseGaps fills each one-pixel gap in a koi drawn alone: water with
// the koi on both sides of it, across or up and down.
func yinYangCloseGaps(px []int8, w int, fill int8) {
	h := len(px) / w
	koi := func(i int) bool { return px[i] != yinYangWater }
	for y := range h {
		for x := range w {
			i := y*w + x
			if !koi(i) && ((x > 0 && x < w-1 && koi(i-1) && koi(i+1)) ||
				(y > 0 && y < h-1 && koi(i-w) && koi(i+w))) {
				px[i] = fill
			}
		}
	}
}

// addStripEyes gives a strip koi level eyes either side of the head at
// (hx, hy), a pixel in from its edge, where on the edge they would only
// narrow the head. A head too narrow gets none.
func addStripEyes(px []int8, w, hx, hy int, body int8) {
	h := len(px) / w
	ex := hx + 1
	for e := hy; e > 1; e-- {
		if ex < w && hy+e < h && px[(hy-e)*w+ex] == body && px[(hy+e)*w+ex] == body {
			px[(hy-e+1)*w+ex] = yinYangWater
			px[(hy+e-1)*w+ex] = yinYangWater
			return
		}
	}
}

// drawShape draws a koi's discs on a copy of its spine. A swimming koi sways
// its rear half with its stroke; strip koi are too small to.
func (d *yinYangDriver) drawShape(k *yinYangKoi, pat yinYangPattern, swim bool) {
	j := k.joints
	size := d.size

	wag := 0.5
	if swim {
		wag = 0.6 * k.stroke
		for i := 4; i < yinYangJoints; i++ {
			a, b := k.joints[i-1], k.joints[i]
			dx, dy := a.x-b.x, a.y-b.y
			if dist := math.Hypot(dx, dy); dist > 0 {
				f := float64(i-3) / (yinYangJoints - 4)
				off := math.Sin(k.phase-f*2.5) * f * f * k.stroke * 1.8 * size
				j[i].x -= dy / dist * off
				j[i].y += dx / dist * off
			}
		}
	}
	if k.flick > 0 {
		bend := k.tailBend() * k.flickDir
		root, prev := j[yinYangJoints-5], j[yinYangJoints-6]
		hx, hy := root.x-prev.x, root.y-prev.y
		if dist := math.Hypot(hx, hy); dist > 0 {
			px, py := -hy/dist, hx/dist
			for i := yinYangJoints - 5; i < yinYangJoints; i++ {
				off := float64(i-(yinYangJoints-6)) / 5 * bend * size * 1.5
				j[i].x += px * off
				j[i].y += py * off
			}
		}
	}

	head := math.Atan2(j[0].y-j[2].y, j[0].x-j[2].x)
	flap := math.Sin(k.phase*0.5) * 0.25
	for _, side := range [2]float64{-1, 1} {
		a := head + math.Pi + side*(1.25+flap)
		for t := range 3 {
			off := (yinYangRadii[2] + 0.6 + float64(t)) * size
			d.fill(j[2].x+math.Cos(a)*off, j[2].y+math.Sin(a)*off, (1.5-float64(t)*0.35)*size, pat.body)
		}
	}
	back := math.Atan2(j[7].y-j[6].y, j[7].x-j[6].x)
	for _, side := range [2]float64{-1, 1} {
		a := back + side*1.9
		d.fill(j[7].x+math.Cos(a)*1.6*size, j[7].y+math.Sin(a)*1.6*size, 0.8*size, pat.body)
	}
	last, before := j[yinYangJoints-1], j[yinYangJoints-2]
	tail := math.Atan2(last.y-before.y, last.x-before.x) + math.Sin(k.phase-2)*wag
	d.fill(last.x+math.Cos(tail)*1.2*size, last.y+math.Sin(tail)*1.2*size, 0.75*size, pat.body)
	d.fill(last.x+math.Cos(tail)*0.6*size, last.y+math.Sin(tail)*0.6*size, 0.65*size, pat.body)

	// Filling between joints too keeps the thin tail whole.
	for i := yinYangJoints - 1; i >= 0; i-- {
		d.fill(j[i].x, j[i].y, yinYangRadii[i]*size, pat.body)
		if i > 0 {
			r := (yinYangRadii[i] + yinYangRadii[i-1]) / 2 * size
			d.fill((j[i].x+j[i-1].x)/2, (j[i].y+j[i-1].y)/2, r, pat.body)
		}
	}
	for i := yinYangJoints - 1; i >= 0; i-- {
		if p := pat.back[i]; p != yinYangWater {
			d.fill(j[i].x, j[i].y, yinYangRadii[i]*size*0.62, p)
		}
	}
}

// yinYangANSI holds the style of every pair of tags a cell can show, one in
// the foreground and one in the background. ApplyThemeColors rebuilds it.
var yinYangANSI [yinYangTagCount][yinYangTagCount]styleANSI

func refreshYinYangANSI() {
	tags := [yinYangTagCount]color.Color{
		yinYangDim:  ColorDim,
		yinYangPad:  SpectrumLow,
		yinYangRed:  SpectrumHigh,
		yinYangInk:  ColorText,
		yinYangGold: SpectrumMid,
	}
	for fg := yinYangDim; fg < yinYangTagCount; fg++ {
		yinYangANSI[fg][yinYangWater] = foregroundANSI(tags[fg])
		for bg := yinYangDim; bg < yinYangTagCount; bg++ {
			yinYangANSI[fg][bg] = colorANSI(lipgloss.NewStyle().Foreground(tags[fg]).Background(tags[bg]))
		}
	}
}

// sextantRune is the block for a 2x3 pattern, bit 0 top left to bit 5
// bottom right.
func sextantRune(p int) rune {
	switch p {
	case 0:
		return ' '
	case 21:
		return '▌'
	case 42:
		return '▐'
	case 63:
		return '█'
	}
	r := rune(0x1FB00 + p - 1)
	if p > 21 {
		r--
	}
	if p > 42 {
		r--
	}
	return r
}

// encode packs the pixels, two across and four down a cell so they come out
// square, into sextants: 2x3 solid blocks, two colours a cell. The top and
// bottom thirds take the top and bottom pixel rows; the middle third, which
// spans the two middle rows, keeps water where they disagree about it.
func (d *yinYangDriver) encode(rows, cols int) string {
	middle := func(a, b int8) int8 {
		switch {
		case a == b:
			return a
		case a == yinYangWater || b == yinYangWater:
			return yinYangWater
		}
		return min(a, b)
	}
	var sb, run strings.Builder
	for row := range rows {
		if row > 0 {
			sb.WriteByte('\n')
		}
		var style styleANSI
		for col := range cols {
			i := row*4*d.w + col*2
			var sub [6]int8
			empty := true
			for sc := range 2 {
				r0, r1, r2, r3 := d.px[i+sc], d.px[i+d.w+sc], d.px[i+2*d.w+sc], d.px[i+3*d.w+sc]
				sub[sc], sub[2+sc], sub[4+sc] = r0, middle(r1, r2), r3
				empty = empty && r0|r1|r2|r3 == yinYangWater
			}
			if empty {
				if style != (styleANSI{}) {
					writeStyledRun(&sb, &run, style)
					style = styleANSI{}
				}
				run.WriteByte(' ')
				continue
			}
			var cellCount [yinYangTagCount]int
			for _, t := range sub {
				cellCount[t]++
			}
			bg, fg := yinYangWater, int8(-1)
			if cellCount[yinYangWater] == 0 {
				bg = yinYangDim
				for t := yinYangDim + 1; t < yinYangTagCount; t++ {
					if cellCount[t] > cellCount[bg] {
						bg = t
					}
				}
			}
			for t := yinYangDim; t < yinYangTagCount; t++ {
				if t != bg && cellCount[t] > 0 && (fg < 0 || cellCount[t] > cellCount[fg]) {
					fg = t
				}
			}
			glyph := ' '
			var next styleANSI
			switch {
			case fg < 0 && bg == yinYangWater:
			case fg < 0:
				glyph, next = '█', yinYangANSI[bg][yinYangWater]
			default:
				p := 0
				for i, t := range sub {
					if t != bg && t != yinYangWater {
						p |= 1 << i
					}
				}
				glyph = sextantRune(p)
				next = yinYangANSI[fg][bg]
				if bg == yinYangWater {
					next = yinYangANSI[fg][yinYangWater]
				}
			}
			if next != style {
				writeStyledRun(&sb, &run, style)
				style = next
			}
			run.WriteRune(glyph)
		}
		writeStyledRun(&sb, &run, style)
	}
	return sb.String()
}
