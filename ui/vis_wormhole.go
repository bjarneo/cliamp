package ui

import (
	"image/color"
	"math"
	"slices"
	"strings"
	"time"
)

// Wormhole: a wireframe tunnel. Rings leave the mouth in the middle, one per
// kick when the beat is steady, and grow past the edges. Kick, snare and
// hi-hat each set off an effect.

const (
	// Speed in laps/s (one lap = tunnel depth). Top speed keeps a frame under
	// 1/4 ring spacing, or the rings seem to run backwards.
	wormholeCruise    = 0.15
	wormholeLoudGain  = 0.1
	wormholeTopSpeed  = 0.45
	wormholeSpeedRate = 2.0
	wormholeFall      = 2.5
	wormholeLoudRate  = 1.5

	// Tempo: median of the last kick gaps, folded into 60..180 BPM.
	wormholeGaps      = 8
	wormholeSureStep  = 0.25
	wormholeSureWait  = 2.0 // s without a kick before the confidence fades
	wormholeLock      = 0.3 // speed change per ring of phase error
	wormholeLockMax   = 0.15
	wormholeTwistBeat = 0.06 // rad/s of wall rotation per beat/s
	wormholeSpiral    = 0.04 // rad per unit of 1/z, per beat/s

	// Rings on screen.
	wormholeRings = 10
	// Tunnel size at depth 1 as a share of the panel.
	wormholeMouth  = 0.12
	wormholeFar    = 0.85
	wormholeNear   = 0.08
	wormholeInner  = 1 / wormholeFar // the same span in 1/z
	wormholeReach  = 1/wormholeNear - wormholeInner
	wormholeShown  = 0.7  // rings fade in until this depth
	wormholeBright = 0.45 // rings nearer than this are bright
	wormholeFlat   = 0.35 // min height/width
	// A ring shows at the mouth this many ring spacings after it starts.
	wormholeEntry = (1 - wormholeFar) * wormholeRings

	// Bend of the far end and rotation of the wall.
	wormholeBend      = 0.2
	wormholeBendRate  = 0.08
	wormholeBendLoud  = 0.25
	wormholeTwist     = 0.05
	wormholeTwistLoud = 0.15

	// Spectral rings: bulge by up to this share where the band was loud.
	wormholeSpectral = 0.3
	wormholeShapes   = wormholeRings + 2

	// Cross-section: circle, hexagon, square, star.
	wormholeForms     = 4
	wormholeToneRelax = 0.01
	wormholeToneSpan  = 0.05
	wormholeFormRate  = 0.4
	wormholeStarDepth = 0.3

	// Hyperspace: the short energy average jumps above the long one.
	wormholeFastRate  = 4.0
	wormholeSlowRate  = 0.15
	wormholeDropRatio = 1.5
	wormholeDropFloor = 0.12
	wormholeDropGap   = 8.0 // s between drops
	wormholeDropWarm  = 4.0 // s of music before a drop counts
	wormholeSilence   = 0.02
	wormholeQuiet     = 0.5 // s of silence before the tunnel stops and holds its bend and twist
	wormholeWarpRise  = 0.15
	wormholeWarpFade  = 0.6
	wormholeWarpLife  = 2.5
	wormholeWarpShown = 0.05
	wormholeWarpWiden = 0.25
	wormholeWarpSmear = 0.8 // in 1/z
	wormholeStars     = 40
	wormholeStarRate  = 3.0 // laps/s of the star field at full warp
	wormholeStarTail  = 4.0 // in 1/z at full warp

	// A hit as strong as the recent ones gets this strength.
	wormholeHitTypical = 0.6
	// Bass shock wave; width in 1/z.
	wormholeWaveTime  = 0.5
	wormholeWaveWidth = 0.7
	wormholeSwell     = 0.15
	wormholeWaveLit   = 0.4
	wormholeWaveHot   = 0.75

	// Snare: rock the wall, alternating sides.
	wormholeRock = 0.1
	// Hi-hat: sparks along the wall.
	wormholeSparkHit  = 6
	wormholeSparkNear = 0.2
	wormholeSparkFar  = 0.5
	wormholeSparkLife = 0.25
	wormholeSparkTail = 0.15
	wormholeSparkRush = 0.6
	wormholeSparkArc  = 2 * math.Pi / 3 // the top third, where the rings show the treble
	wormholeSparks    = 18
	wormholeWaves     = 4
	wormholeMaxSteps  = 512 // longest streak in dots

	// Per-band min/max tracking.
	wormholeBandRelax = 0.08
	wormholeBandSpan  = 0.12
)

const (
	wormholeBass = iota
	wormholeMids
	wormholeTreble
	wormholeParts
)

// Cell tiers, low to high; a cell shows the highest one drawn into it.
const (
	wormholeDim int8 = iota + 1
	wormholeText
	wormholeBeat
	wormholeHot
)

const wormholeTiers = int(wormholeHot)

// Rebuilt by ApplyThemeColors.
var wormholeANSI [wormholeTiers]styleANSI

func refreshWormholeANSI() {
	for i, c := range [wormholeTiers]color.Color{ColorDim, ColorText, SpectrumMid, SpectrumHigh} {
		wormholeANSI[i] = foregroundANSI(c)
	}
}

func flushWormholeRun(sb, run *strings.Builder, tier int) {
	var style styleANSI
	if tier > 0 && tier <= len(wormholeANSI) {
		style = wormholeANSI[tier-1]
	}
	writeStyledRun(sb, run, style)
}

type wormholeDriver struct {
	spectrumDriverBase

	grid     brailleGrid
	frame    wormholeFrame
	travel   float64 // laps
	speed    float64 // laps/s
	loud     float64
	lo, hi   [DefaultSpectrumBands]float64
	lastTick time.Time

	bendPhase, twist float64
	onsets           [wormholeParts]wormholeOnset
	prev             [DefaultSpectrumBands]float64
	hasPrev          bool
	typical          [wormholeParts]float64 // recent hit ratios
	clock            float64                // playback seconds, frozen while paused

	// Tempo: kick gaps and beat period in s (0 = none yet), confidence 0..1.
	gaps           [wormholeGaps]float64
	counted        int
	lastKick, beat float64
	sure, spiral   float64

	// Live band levels, and a snapshot per ring taken at the mouth.
	glow   [DefaultSpectrumBands]float64
	shapes [wormholeShapes][DefaultSpectrumBands]float64
	born   int

	tone, toneLo, toneHi  float64
	form, formShift, jump float64

	fastE, slowE float64
	warpAt, warp float64
	starTravel   float64
	heard        float64 // s of music since the last silence
	quiet        float64 // s of silence

	waves    [wormholeWaves]wormholeWave
	nextWave int

	rock, rockTo, rockSide float64

	sparks [wormholeSparks]wormholeSpark
	struck int // sparks so far, also seeds the next spark's position
}

// wormholeOnset finds a sudden rise across a share of the bands, judged
// against how much they usually rise. Hits are at least gap ticks apart.
type wormholeOnset struct {
	from, to   float64
	k, floor   float64
	gap, since int
	mean, dev  float64
}

func (o *wormholeOnset) reset() {
	o.mean, o.dev, o.since = 0.03, 0.02, o.gap
}

// step reports a hit and its strength, 1 at the threshold.
func (o *wormholeOnset) step(bands, prev []float64) (bool, float64) {
	n := len(bands)
	rise := 0.0
	for i := int(o.from * float64(n)); i < int(o.to*float64(n)) && i < len(prev); i++ {
		rise += max(0, bands[i]-prev[i])
	}
	threshold := max(o.mean+o.k*o.dev, o.floor)
	hit := rise > threshold && o.since >= o.gap
	o.mean = o.mean*0.92 + rise*0.08
	o.dev = o.dev*0.92 + math.Abs(rise-o.mean)*0.08
	if hit {
		o.since = 0
	} else {
		o.since++
	}
	return hit, rise / threshold
}

// wormholeHash returns a fixed pseudo-random value in [0, 1) for item i, a
// different one for each slot.
func wormholeHash(i, slot int) float64 {
	h := uint64(i)*7919 + uint64(slot)*104729
	h ^= h >> 16
	h *= 0x45d9f3b37197344b
	h ^= h >> 16
	return float64(h%10000) / 10000
}

// wormholeFrame is the projection of the frame that Render draws.
type wormholeFrame struct {
	halfW, halfH float64
	rx, ry       float64 // mouth radii in dots
	bendX, bendY float64
	formI        int // shape, and its blend into the next one
	formF        float64
}

type wormholeWave struct{ at, strength float64 } // strength 0 = unused

type wormholeSpark struct{ angle, z, at, travel float64 }

func newWormholeDriver() visModeDriver {
	d := &wormholeDriver{}
	d.OnEnter(nil)
	return d
}

func (d *wormholeDriver) OnEnter(*Visualizer) {
	*d = wormholeDriver{}
	for i := range d.lo {
		d.lo[i] = 1 // start wide open
	}
	d.rockSide = 1
	// Gaps in 25 ms ticks: at most 240 kicks a minute.
	d.onsets = [wormholeParts]wormholeOnset{
		wormholeBass:   {from: 0, to: 0.3, k: 2.0, floor: 0.05, gap: 10},
		wormholeMids:   {from: 0.3, to: 0.6, k: 2.0, floor: 0.04, gap: 8},
		wormholeTreble: {from: 0.6, to: 1, k: 1.8, floor: 0.025, gap: 6},
	}
	for i := range d.onsets {
		d.onsets[i].reset()
	}
	for i := range d.sparks {
		d.sparks[i].at = math.Inf(-1)
	}
	d.lastKick, d.warpAt = math.Inf(-1), math.Inf(-1)
	d.born = wormholeBorn(0)
	d.toneLo = 1
	d.quiet = wormholeQuiet
}

// Audible tap so the effects line up with what you hear.
func (*wormholeDriver) AnalysisSpec(*Visualizer) VisAnalysisSpec {
	spec := spectrumAnalysisSpec(DefaultSpectrumBands)
	spec.Tap = VisTapAudible
	return spec
}

func (d *wormholeDriver) pauseSettled() bool { return d.speed == 0 }

// Tick twice per rendered frame, so every frame shows a fresh step.
func (*wormholeDriver) TickInterval(_ *Visualizer, ctx VisTickContext) time.Duration {
	if ctx.Playing && !ctx.OverlayActive {
		return TickRawSample
	}
	return defaultDriverTickInterval(ctx)
}

func (*wormholeDriver) ownsCadence() {}

func (d *wormholeDriver) Tick(v *Visualizer, ctx VisTickContext) {
	defaultDriverTick(v, ctx, d.AnalysisSpec(v))
	if ctx.OverlayActive {
		// Suspend() also sends this while paused, so keep the speed.
		d.lastTick = time.Time{}
		return
	}
	if !ctx.Now.IsZero() && !ctx.Now.After(d.lastTick) {
		return
	}
	dt := clampFrameDT(ctx.Now, d.lastTick, d.TickInterval(v, ctx)).Seconds()
	d.lastTick = ctx.Now

	switch {
	case ctx.Paused:
		d.speed = wormholeEase(d.speed, 0, wormholeFall, dt)
		if d.speed < 0.01 {
			d.speed = 0
		}
	case ctx.Playing:
		bands := v.SmoothedBands()
		d.loud = wormholeEase(d.loud, d.levels(bands, dt), wormholeLoudRate, dt)
		d.clock += dt
		d.hits(bands)
		d.rock = wormholeEase(d.rock, d.rockTo, 14, dt)
		if d.clock-d.lastKick > wormholeSureWait {
			d.sure = max(0, d.sure-0.5*dt)
		}
		energy, tone, heard := wormholeTimbre(bands)
		d.morph(tone, heard, dt)
		d.dive(energy, dt)

		d.speed = wormholeEase(d.speed, d.targetSpeed(), wormholeSpeedRate, dt)
		beats := d.beatRate()
		d.spiral = wormholeEase(d.spiral, wormholeSpiral*beats, 0.5, dt)
		if d.quiet < wormholeQuiet {
			d.bendPhase += (wormholeBendRate + wormholeBendLoud*d.loud) * dt
			d.twist += (wormholeTwist + wormholeTwistLoud*d.loud + wormholeTwistBeat*beats) * dt
		}
		d.starTravel += wormholeStarRate * d.warp * dt
	default:
		// Stopped: ticks are too far apart to ease, just stop.
		d.speed = 0
	}
	d.travel += d.speed * dt
	d.keepShapes()
}

func wormholeEase(cur, target, rate, dt float64) float64 {
	return easeToward(cur, target, rate, rate, dt)
}

// keepShapes snapshots the glow for each ring that reached the mouth.
func (d *wormholeDriver) keepShapes() {
	newest := wormholeBorn(d.travel)
	for d.born < newest {
		d.born++
		d.shapes[wormholeSlot(d.born)] = d.glow
	}
}

// beatRate is the tempo in beats/s, weighted by how sure it is.
func (d *wormholeDriver) beatRate() float64 {
	if d.beat == 0 {
		return 0
	}
	return d.sure / d.beat
}

// targetSpeed is one ring per beat when the tempo is sure, else the cruise.
// The tunnel stops when there is nothing to hear.
func (d *wormholeDriver) targetSpeed() float64 {
	if d.quiet >= wormholeQuiet {
		return 0
	}
	target := wormholeCruise + wormholeLoudGain*d.loud
	if d.beat > 0 {
		// Phase error in rings: 0 when a ring reaches the mouth on the kick.
		ring := d.travel*wormholeRings - wormholeEntry
		err := ring - (d.clock-d.lastKick)/d.beat
		err -= math.Round(err)
		lock := max(-wormholeLockMax, min(wormholeLockMax, -wormholeLock*err)) * (1 - d.warp)
		beat := (1 + lock) / (d.beat * wormholeRings)
		target += (beat - target) * d.sure
	}
	return target + (wormholeTopSpeed-target)*d.warp
}

// hits finds kick, snare and hi-hat as rises, not levels: a louder bass
// floor must not hide the kicks.
func (d *wormholeDriver) hits(bands []float64) {
	n := min(len(bands), len(d.prev))
	prev := d.prev
	copy(d.prev[:], bands[:n])
	if !d.hasPrev {
		d.hasPrev = true
		return
	}
	for part := range d.onsets {
		hit, ratio := d.onsets[part].step(bands[:n], prev[:n])
		if !hit {
			continue
		}
		// Judge the hit against the recent ones, or every kick looks hard.
		strength := wormholeHitTypical
		if d.typical[part] > 0 {
			strength = min(1, wormholeHitTypical*ratio/d.typical[part])
			d.typical[part] += (ratio - d.typical[part]) * 0.2
		} else {
			d.typical[part] = ratio
		}
		switch part {
		case wormholeBass:
			d.kick(strength)
		case wormholeMids:
			d.rockTo, d.rockSide = d.rockSide*wormholeRock, -d.rockSide
		default:
			d.strike(strength)
		}
	}
}

func (d *wormholeDriver) kick(strength float64) {
	d.waves[d.nextWave] = wormholeWave{at: d.clock, strength: strength}
	d.nextWave = (d.nextWave + 1) % wormholeWaves

	gap := d.clock - d.lastKick
	d.lastKick = d.clock
	if gap < 0.25 || gap > 2 {
		return // no beat
	}
	// Fold into 60..180 BPM.
	for gap < 60.0/180 {
		gap *= 2
	}
	for gap > 60.0/60 {
		gap /= 2
	}
	// Confidence grows only while the kicks agree with the tempo.
	if d.beat == 0 || math.Abs(gap-d.beat) < 0.1*d.beat {
		d.sure = min(1, d.sure+wormholeSureStep)
	} else {
		d.sure = max(0, d.sure-wormholeSureStep/2)
	}
	d.gaps[d.counted%wormholeGaps] = gap
	d.counted++
	sorted := d.gaps
	n := min(d.counted, wormholeGaps)
	slices.Sort(sorted[:n])
	d.beat = sorted[n/2]
}

// strike throws sparks at the top of the wall, where the rings show the
// treble. From there they turn with the wall.
func (d *wormholeDriver) strike(strength float64) {
	for range max(1, int(math.Round(strength*wormholeSparkHit))) {
		z := wormholeSparkNear + (wormholeSparkFar-wormholeSparkNear)*wormholeHash(d.struck, 1)
		top := -math.Pi/2 + (wormholeHash(d.struck, 0)-0.5)*wormholeSparkArc
		d.sparks[d.struck%wormholeSparks] = wormholeSpark{
			angle:  top - d.twist - d.rock - d.spiral/z,
			z:      z,
			at:     d.clock,
			travel: d.travel,
		}
		d.struck++
	}
}

// levels normalises each band against its own recent range and returns the
// overall loudness, 0..1.
func (d *wormholeDriver) levels(bands []float64, dt float64) (loud float64) {
	n := min(len(bands), len(d.lo))
	for i, b := range bands[:n] {
		d.hi[i] = max(b, d.hi[i]-wormholeBandRelax*dt)
		d.lo[i] = min(b, d.lo[i]+wormholeBandRelax*dt)
		level := (b - d.lo[i]) / max(d.hi[i]-d.lo[i], wormholeBandSpan)
		d.glow[i] = easeToward(d.glow[i], level, 12, 6, dt)
		loud += level / float64(n)
	}
	return loud
}

// wormholeTimbre returns the mean level and the tone, 0 for all bass to 1 for
// all treble. Near silence has no tone.
func wormholeTimbre(bands []float64) (energy, tone float64, heard bool) {
	sum, moment := 0.0, 0.0
	for i, b := range bands {
		sum += b
		moment += float64(i) * b
	}
	energy = sum / float64(max(1, len(bands)))
	if sum <= 1e-3 || len(bands) < 2 {
		return energy, 0, false
	}
	return energy, moment / sum / float64(len(bands)-1), true
}

// morph picks the cross-section from the tone, against its recent range.
func (d *wormholeDriver) morph(tone float64, heard bool, dt float64) {
	if heard {
		if d.toneHi < d.toneLo {
			// The first sound sets the range.
			d.tone, d.toneLo, d.toneHi = tone, tone, tone
		}
		d.tone = wormholeEase(d.tone, tone, 1, dt)
		d.toneHi = max(d.tone, d.toneHi-wormholeToneRelax*dt)
		d.toneLo = min(d.tone, d.toneLo+wormholeToneRelax*dt)
		target := (d.tone - d.toneLo) / max(d.toneHi-d.toneLo, wormholeToneSpan) * (wormholeForms - 1)
		d.form = wormholeEase(d.form, target, wormholeFormRate, dt)
	}
	d.formShift = wormholeEase(d.formShift, d.jump, 3, dt)
}

// dive finds drops in the raw energy and runs the hyperspace envelope.
func (d *wormholeDriver) dive(energy, dt float64) {
	if energy < wormholeSilence {
		d.heard = 0
		d.quiet += dt
	} else {
		d.quiet = 0
		if d.heard == 0 {
			// A track rising out of silence is no drop.
			d.fastE, d.slowE = energy, energy
		}
		d.heard += dt
	}
	d.fastE = wormholeEase(d.fastE, energy, wormholeFastRate, dt)
	// While warming up the long average learns the level as fast as the short one.
	slow := wormholeSlowRate
	if d.heard < wormholeDropWarm {
		slow = wormholeFastRate
	}
	d.slowE = wormholeEase(d.slowE, energy, slow, dt)
	drop := d.fastE > wormholeDropRatio*d.slowE && d.fastE > wormholeDropFloor
	if drop && d.heard >= wormholeDropWarm && d.clock-d.warpAt > wormholeDropGap {
		d.warpAt = d.clock
		d.jump++
	}
	d.warp = wormholeWarpAt(d.clock - d.warpAt)
}

// wormholeWarpAt is the hyperspace envelope, age seconds after a drop.
func wormholeWarpAt(age float64) float64 {
	switch {
	case age < 0 || age > wormholeWarpLife:
		return 0
	case age < wormholeWarpRise:
		return age / wormholeWarpRise
	}
	return math.Exp(-(age - wormholeWarpRise) / wormholeWarpFade)
}

// wormholeBorn is the newest ring that has reached the mouth.
func wormholeBorn(travel float64) int {
	return int(math.Floor(travel*wormholeRings - wormholeEntry))
}

func wormholeSlot(ring int) int {
	return (ring%wormholeShapes + wormholeShapes) % wormholeShapes
}

// spectrum is the bulge of the wall at screen angle sin and depth z, 0..1.
func (d *wormholeDriver) spectrum(sin, z float64) float64 {
	u := math.Acos(max(-1, min(1, sin))) / math.Pi * float64(DefaultSpectrumBands-1)
	n := (z - 1 + d.travel) * wormholeRings
	ring := math.Floor(n)
	a, b := d.ringLevel(int(ring), u), d.ringLevel(int(ring)+1, u)
	return a + (b-a)*(n-ring)
}

func (d *wormholeDriver) ringLevel(ring int, u float64) float64 {
	bands := &d.glow
	switch {
	case ring <= d.born-wormholeShapes:
		return 0
	case ring <= d.born:
		bands = &d.shapes[wormholeSlot(ring)]
	}
	return sampleBandLinear(bands[:], u)
}

// wormholeBlend is the radius at wall angle a of shape i mixed with the next
// one by f, at most 1.
func wormholeBlend(i int, f, a float64) float64 {
	cur := wormholeShape(i, a)
	if f == 0 {
		return cur
	}
	return cur + (wormholeShape((i+1)%wormholeForms, a)-cur)*f
}

func wormholeShape(kind int, a float64) float64 {
	switch kind {
	case 1:
		return wormholePolygon(6, a)
	case 2:
		return wormholePolygon(4, a)
	case 3:
		return 1 / (1 + wormholeStarDepth*(1-math.Cos(5*a)))
	}
	return 1
}

// wormholePolygon is the radius of a regular n-gon with corners at 1.
func wormholePolygon(n int, a float64) float64 {
	seg := 2 * math.Pi / float64(n)
	return math.Cos(seg/2) / math.Cos(wormholeWrap(a, seg)-seg/2)
}

// wormholeWrap returns x modulo m in [0, m), also for a negative x.
func wormholeWrap(x, m float64) float64 {
	return x - m*math.Floor(x/m)
}

// swell is how much the shock waves widen the tunnel at depth z.
func (d *wormholeDriver) swell(z float64) float64 {
	share := 0.0
	for _, w := range d.waves {
		front, ok := w.front(d.clock)
		if !ok {
			continue
		}
		off := (1/z - front) / wormholeWaveWidth
		share = max(share, w.strength*math.Exp(-off*off))
	}
	return wormholeSwell * share
}

// front returns the 1/z of the wave front; ok is false once it is done.
func (w wormholeWave) front(clock float64) (invZ float64, ok bool) {
	age := clock - w.at
	if w.strength == 0 || age < 0 || age > wormholeWaveTime {
		return 0, false
	}
	return wormholeInner + wormholeReach*age/wormholeWaveTime, true
}

// bend returns the offset of the far end as a share of the panel.
func (d *wormholeDriver) bend() (x, y float64) {
	reach := wormholeBend * (1 + 0.5*d.loud)
	return reach * math.Sin(d.bendPhase), 0.6 * reach * math.Sin(0.7*d.bendPhase)
}

// at maps wall angle a and depth z to a screen dot. The spiral turns the wall
// more the nearer it is.
func (d *wormholeDriver) at(a, z, scale float64) (float64, float64) {
	f := &d.frame
	sin, cos := math.Sincos(a + d.spiral/z)
	grow := scale * wormholeBlend(f.formI, f.formF, a) * (1 + wormholeSpectral*d.spectrum(sin, z)) / z
	return f.halfW*(1+f.bendX*z*z) + f.rx*cos*grow, f.halfH*(1+f.bendY*z*z) + f.ry*sin*grow
}

func (d *wormholeDriver) Render(v *Visualizer) string {
	rows, cols := v.Rows, v.columns()
	if rows <= 0 || cols <= 0 {
		return strings.Repeat("\n", max(0, rows-1))
	}
	w, h := cols*2, rows*4
	d.grid.flush, d.grid.keepRunOnEmpty = flushWormholeRun, true
	d.grid.ensure(h, w)

	f := &d.frame
	f.halfW, f.halfH = float64(w)/2, float64(h)/2
	tunnelH := max(f.halfH, wormholeFlat*f.halfW)
	// At least 5 dots, or the far rings merge in short panels.
	mouth := max(wormholeMouth, 5/tunnelH) * (1 + wormholeWarpWiden*d.warp)
	f.rx, f.ry = mouth*f.halfW, mouth*tunnelH
	f.bendX, f.bendY = d.bend()
	form := wormholeWrap(d.form+d.formShift, wormholeForms)
	f.formI, f.formF = int(form), form-math.Floor(form)

	// One wall line per 4 dot rows. Step evenly in 1/z so the dots are evenly
	// spaced.
	lines := max(8, min(16, h/4))
	for line := range lines {
		a := 2*math.Pi*float64(line)/float64(lines) + d.twist + d.rock
		x0, y0 := d.at(a, wormholeFar, 1)
		x1, y1 := d.at(a, wormholeNear, 1)
		steps := int(max(math.Abs(x1-x0), math.Abs(y1-y0))) + 1
		for s := range steps + 1 {
			t := float64(s) / float64(steps)
			z := 1 / (wormholeInner + wormholeReach*t)
			x, y := d.at(a, z, 1+d.swell(z))
			d.grid.set(int(x), int(y), wormholeDim)
		}
	}

	// Ring n enters at the mouth when travel reaches n/wormholeRings.
	newest := int(math.Floor(d.travel * wormholeRings))
	for ring := newest - wormholeRings + 1; ring <= newest; ring++ {
		z := 1 - d.travel + float64(ring)/wormholeRings
		if z > wormholeFar || z < wormholeNear {
			continue
		}
		swell := d.swell(z)
		tier := wormholeDim
		switch {
		case swell > wormholeWaveHot*wormholeSwell:
			tier = wormholeHot
		case swell > wormholeWaveLit*wormholeSwell || d.warp > 0.5:
			tier = wormholeBeat
		case z < wormholeBright:
			tier = wormholeText
		}
		shown := max(0, min(1, (wormholeFar-z)/(wormholeFar-wormholeShown))) // fade in at the mouth
		d.drawRing(z, shown, tier)
	}

	// Wave fronts.
	for _, w := range d.waves {
		front, ok := w.front(d.clock)
		if !ok {
			continue
		}
		tier := wormholeBeat
		if w.strength >= wormholeWaveHot {
			tier = wormholeHot
		}
		d.drawRing(1/front, 1, tier)
	}

	// Sparks.
	for _, s := range d.sparks {
		age := d.clock - s.at
		head := s.z - (d.travel - s.travel) - wormholeSparkRush*age
		if age > wormholeSparkLife || head < wormholeNear {
			continue
		}
		a := s.angle + d.twist + d.rock
		tail := head + wormholeSparkTail
		x0, y0 := d.at(a, head, 1+d.swell(head))
		x1, y1 := d.at(a, tail, 1+d.swell(tail))
		d.streak(x0, y0, x1, y1, wormholeText, wormholeBeat)
	}

	// Star streaks in hyperspace.
	if d.warp > wormholeWarpShown {
		for i := range wormholeStars {
			head := wormholeInner + wormholeReach*math.Mod(wormholeHash(i, 2)+d.starTravel, 1)
			tail := max(wormholeInner, head-wormholeStarTail*d.warp)
			a := 2*math.Pi*wormholeHash(i, 3) + d.twist
			reach := 0.4 + 0.5*wormholeHash(i, 4)
			x0, y0 := d.at(a, 1/head, reach)
			x1, y1 := d.at(a, 1/tail, reach)
			d.streak(x0, y0, x1, y1, wormholeHot, wormholeBeat)
		}
	}
	return d.grid.render(rows, cols)
}

// streak draws a line whose first third is tier head and the rest tier tail.
// Capped, so a wild projection stays cheap.
func (d *wormholeDriver) streak(x0, y0, x1, y1 float64, head, tail int8) {
	span := max(math.Abs(x1-x0), math.Abs(y1-y0))
	if math.IsNaN(span) || math.IsInf(span, 0) {
		return
	}
	steps := min(wormholeMaxSteps, int(span)+1)
	for i := range steps + 1 {
		t := float64(i) / float64(steps)
		tier := tail
		if 3*i < steps {
			tier = head
		}
		d.grid.set(int(x0+(x1-x0)*t), int(y0+(y1-y0)*t), tier)
	}
}

// drawRing draws the fraction shown of the ring at depth z. In hyperspace
// every few dots smear back towards the mouth.
func (d *wormholeDriver) drawRing(z, shown float64, tier int8) {
	swell := d.swell(z)
	points := int(2*math.Pi*max(d.frame.rx, d.frame.ry)*(1+swell)*(1+wormholeSpectral)/z) + 8
	smear := 1 / max(wormholeInner, 1/z-wormholeWarpSmear*d.warp)
	for p := range points {
		// The 7 spreads the dots that show around the ring.
		if float64(p*7%points) >= shown*float64(points) {
			continue
		}
		a := 2*math.Pi*float64(p)/float64(points) + d.twist + d.rock
		x, y := d.at(a, z, 1+swell)
		if d.warp <= wormholeWarpShown || p%max(1, points/16) != 0 {
			d.grid.set(int(x), int(y), tier)
			continue
		}
		x1, y1 := d.at(a, smear, 1+swell)
		d.streak(x, y, x1, y1, tier, tier)
	}
}
