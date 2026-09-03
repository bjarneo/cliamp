/* 
"Void" theme for cliamp.
Renders a starfield and black hole that reacts to audio frequencies.
*/
package ui

import (
	"math"
	"strings"
	"time"
)

// voidDriver renders a deep-space void dominated by a supermassive black hole.
// The hole is drawn as a wide, foreshortened ellipse (the classic tilted-view
// silhouette) that stretches across most of the window, with a dark event
// horizon at its heart wrapped in a hot, spinning accretion disk and a blazing
// photon ring. Around it:
//
//   - Matter continuously spirals inward (accretion) and heats up at the rim.
//   - Ejected orbs are flung outward from the disk and fly all the way across
//     the window, orbiting and trailing as they go.
//   - A gravitational pulse rings outward with every bass hit, crossing the
//     whole window.
//   - Supernovas flare and fade across the sky on strong transients.
//
// The whole scene is driven by the music:
//
//   - Bass        → disk swelling, infall surge, ejection bursts, pulse velocity
//   - Mid         → disk filament shimmer, rotation speed, infall swirl
//   - High/treble → disk sparkle, photon-ring snap, orb/supernova trigger
//   - Overall     → brightness and pulse of the whole apparatus
type voidDriver struct {
	// Gravitational pulse: an expanding, fading ring re-kicked on bass hits.
	shockR, shockStr, shockVel float64

	// Supernovas flare on strong transients.
	novas      []nova
	lastNovaAt uint64

	// Ejected orbs flung outward from the disk, flying across the window.
	orbs      []orb
	lastOrbAt uint64

	prevBass    float64
	prevHigh    float64
	prevMid     float64
	prevLevel   float64
	infallPhase uint64
	orbCounter  uint64
}

// nova is a single distant stellar explosion in flight.
type nova struct {
	bx, by float64 // normalized position (-1..1) across the sky
	birth  uint64
}

// orb is an ejected particle travelling outward from the black hole.
type orb struct {
	angle float64 // launch direction (radians, in normalized space)
	birth uint64
	speed float64 // normalized-radius growth per frame
	seed  uint64
}

func newVoidDriver() visModeDriver {
	return &voidDriver{}
}

func (*voidDriver) AnalysisSpec(*Visualizer) VisAnalysisSpec {
	return spectrumAnalysisSpec(DefaultSpectrumBands)
}

// voidMetrics buckets a handful of derived audio values used across Tick/Render.
type voidMetrics struct {
	bass, mid, high, avg, peak, level float64
}

// computeMetrics derives frequency buckets from the smoothed bands.
func computeMetrics(bands []float64) voidMetrics {
	var m voidMetrics
	n := len(bands)
	if n <= 0 {
		return m
	}
	m.avg = 0
	m.peak = 0
	for _, b := range bands {
		m.avg += b
		if b > m.peak {
			m.peak = b
		}
	}
	m.avg /= float64(n)
	m.bass = bandAvg(bands, 0, n/3+1)
	m.mid = bandAvg(bands, n/3, 2*n/3)
	m.high = bandAvg(bands, 2*n/3, n)
	m.level = 0.6*m.avg + 0.4*m.peak
	return m
}

// Tick advances the driver's persistent state: it kicks gravitational pulses,
// launches ejected orbs, and triggers supernovas on transients, then decays the
// pulse ring.
func (d *voidDriver) Tick(v *Visualizer, ctx VisTickContext) {
	defaultDriverTick(v, ctx, d.AnalysisSpec(v))
	if ctx.OverlayActive {
		return
	}

	m := computeMetrics(v.SmoothedBands())

	// Gravitational pulse: kick on every bass hit, accelerate on sustained bass.
	bassJump := m.bass - d.prevBass
	if bassJump > 0.06 {
		// Launch the pulse just outside the photon ring so it visibly propagates
		// outward from the event horizon instead of appearing at the disk edge.
		d.shockR = 0.43
		d.shockStr = math.Min(1.0, 0.62+bassJump*3.2)
		d.shockVel = 0.045 + bassJump*0.10
	} else if d.shockStr > 0.01 {
		d.shockVel = d.shockVel*0.96 + m.bass*0.012
	}
	d.prevBass = m.bass

	// Ejected orbs: a slow ambient trickle plus bursts on bass/high transients.
	highJump := m.high - d.prevHigh
	levelJump := m.level - d.prevLevel
	midJump := m.mid - d.prevMid

	ambient := 0
	if d.orbCounter%2 == 0 && m.level > 0.04 {
		ambient = 1
	}
	burst := 0
	if bassJump > 0.06 {
		burst = 3 + int(m.bass*6)
	}
	if highJump > 0.12 {
		burst++
	}
	if midJump > 0.10 {
		burst++
	}
	var launches int
	if m.level > 0.03 {
		launches = ambient + burst
	}
	if launches > 6 {
		launches = 6
	}
	for i := 0; i < launches && len(d.orbs) < 46; i++ {
		seed := uint64(d.infallPhase)*7919 + uint64(i)*104729 + 13
		baseSpeed := 0.016 + m.bass*0.055
		jitter := rand01(seed) * 0.02
		d.orbs = append(d.orbs, orb{
			angle: rand01(seed+1) * 2 * math.Pi,
			birth: v.frame,
			speed: baseSpeed + jitter,
			seed:  uint64(i) + d.orbCounter,
		})
	}
	// Drop any that have flown out or overflow the cap (oldest first).
	alive := d.orbs[:0]
	for _, o := range d.orbs {
		if v.frame-o.birth < orbLifetime {
			alive = append(alive, o)
		}
	}
	d.orbs = alive
	d.orbCounter++
	d.lastOrbAt = v.frame

	// Supernova: flare on a strong high/treble or overall transient, gated by a
	// short cooldown so they feel like rare cosmic events, not constant noise.
	cooldown := v.frame-d.lastNovaAt > 24
	if cooldown && (highJump > 0.20 || levelJump > 0.30 || bassJump > 0.36) && len(d.novas) < 7 {
		d.novas = append(d.novas, nova{
			bx:    scatterHash(3, int(d.infallPhase%97), 11, v.frame)*2 - 1,
			by:    scatterHash(4, int(d.infallPhase%97), 23, v.frame)*2 - 1,
			birth: v.frame,
		})
		d.lastNovaAt = v.frame
	}
	d.prevHigh = m.high
	d.prevMid = m.mid
	d.prevLevel = m.level

	// Age the pulse ring outward.
	if d.shockStr > 0.01 {
		d.shockR += d.shockVel
		d.shockStr *= 0.94
		if d.shockStr < 0.01 {
			d.shockStr = 0
			d.shockVel = 0
		}
	}
	d.infallPhase++
}

func (*voidDriver) TickInterval(_ *Visualizer, ctx VisTickContext) time.Duration {
	return defaultDriverTickInterval(ctx)
}

func (d *voidDriver) OnEnter(*Visualizer) {
	d.shockR = 0
	d.shockStr = 0
	d.shockVel = 0
	d.prevBass = 0
	d.prevHigh = 0
	d.prevMid = 0
	d.prevLevel = 0
	d.novas = nil
	d.orbs = nil
	d.lastNovaAt = 0
	d.lastOrbAt = 0
	d.infallPhase = 0
	d.orbCounter = 0
}

func (*voidDriver) OnLeave(*Visualizer) {}

// pauseSettled reports whether all transient activity (pulses, orbs, and
// supernovas) has faded so the model can drop the visualizer to an idle tick.
func (d *voidDriver) pauseSettled() bool {
	if d.shockStr > 0.01 {
		return false
	}
	if len(d.novas) > 0 {
		return false
	}
	if len(d.orbs) > 0 {
		return false
	}
	return true
}

var _ visPauseSettler = (*voidDriver)(nil)

const (
	novaLifetime = 48
	orbLifetime  = 90
)

// rand01 returns a deterministic pseudo-random value in [0,1) for a key.
func rand01(seed uint64) float64 {
	h := seed*2246822519 + 3266489917
	h ^= h >> 16
	h *= 0x45d9f3b37197344b
	h ^= h >> 16
	return float64(h%10000) / 10000.0
}

// voidGeom captures the window geometry and the (wide, foreshortened) disk
// ellipse so every sub-render can share one projection.
type voidGeom struct {
	dotCols, dotRows int
	mx, my           float64 // center in dot space
	aX, aY           float64 // disk semi-axes in dots (wide + short)
	kCore            float64 // event-horizon radius, in normalized units
	sway             float64
	R                func(dx, dy float64) float64 // normalized radius
	theta            func(dx, dy float64) float64
}

func (d *voidDriver) Render(v *Visualizer) string {
	height := v.Rows
	dotRows := height * 4
	dotCols := PanelWidth * 2
	if dotRows < 4 || dotCols < 4 {
		return strings.Repeat("\n", max(0, height-1))
	}

	m := computeMetrics(v.SmoothedBands())

	mx := float64(dotCols) / 2.0
	my := float64(dotRows) / 2.0

	// A wide, foreshortened disk: horizontal semi-axis stretches across most of
	// the window while the vertical semi-axis is kept short so the hole reads as
	// a tilted silhouette (not just a flat band).
	aX := float64(dotCols) * 0.36
	aY := float64(dotRows) * 0.52
	kCore := 0.40 // event horizon, normalized

	// Sway so the black hole drifts subtly instead of sitting perfectly centre.
	sway := math.Sin(float64(v.frame)*0.008) * 0.8 * (0.5 + m.level)

	// Normalized-ellipse helpers: R=1 is the disk's outer edge, and everything
	// stays a smooth ellipse regardless of the window's aspect ratio.
	R := func(dx, dy float64) float64 { return math.Hypot(dx/aX, dy/aY) }
	theta := func(dx, dy float64) float64 {
		t := math.Atan2(dy/aY, dx/aX)
		if t < 0 {
			t += 2 * math.Pi
		}
		return t
	}
	g := voidGeom{
		dotCols: dotCols, dotRows: dotRows,
		mx: mx, my: my, aX: aX, aY: aY, kCore: kCore, sway: sway,
		R: R, theta: theta,
	}

	// Disk "breathing": bass swells the outer edge; the hole never disappears.
	breathe := 1.0 + m.bass*0.20 + (m.level-0.5)*0.10

	// Per-dot lit grid plus per-cell strongest color tier.
	lit := make([]bool, dotRows*dotCols)
	cellTag := make([]int8, height*PanelWidth)

	setDot := func(x, y int, tag int) {
		if x < 0 || x >= dotCols || y < 0 || y >= dotRows {
			return
		}
		lit[y*dotCols+x] = true
		idx := (y/4)*PanelWidth + x/2
		if tag > int(cellTag[idx]) {
			cellTag[idx] = int8(tag)
		}
	}

	// --- Starfield: twinkles and densifies with the overall level. Kept sparse
	// so flying orbs aren't mistaken for static stars. ---
	starDensity := 0.035 + m.level*0.06
	for y := 0; y < dotRows; y++ {
		for x := 0; x < dotCols; x++ {
			if scatterHash(0, y, x, 0) > starDensity {
				continue
			}
			if scatterHash(0, y, x, v.frame)*0.5+m.level*0.5 > 0.5 {
				setDot(x, y, 0)
			}
		}
	}

	rotSpeed := 0.03 + (m.mid+m.high)*0.03
	rot := float64(v.frame) * rotSpeed

	// --- Full-grid scan: disk, event horizon, photon ring, gravitational pulse.
	// Span the grid to R_max so the pulse actually crosses the whole window. ---
	rmax := math.Hypot(mx/aX, my/aY) + 0.3
	kDiskE := 1.0 * breathe
	for dy := -my; dy <= my; dy++ {
		for dx := -mx; dx <= mx; dx++ {
			r := R(dx, dy)
			if r > rmax {
				continue
			}
			t := theta(dx, dy)
			x := int(mx + dx)
			y := int(my + sway + dy)

			// --- Dark event horizon: draw nothing (reveals the void). ---
			if r < kCore {
				continue
			}

			// --- Photon ring: a razor-thin, blazing ring just outside the hole.
			// Flickers faster with treble so it snaps and glints. Keep this before
			// the disk branch so the accretion disk cannot hide it. ---
			if math.Abs(r-kCore) < 0.10 {
				glint := scatterHash(9, int(dy), int(dx), v.frame)
				rng := 0.5 + m.high*1.6
				if glint < rng*0.5+m.high*0.5 {
					setDot(x, y, 2)
				}
				continue
			}

			// --- Accretion disk. Rendered as swirling filaments with gaps so
			// ejected orbs remain visible against the structure. ---
			if r <= kDiskE {
				doppler := 0.55 + 0.45*math.Cos(t-math.Pi*0.25)
				filament := 0.5 + 0.5*math.Cos(t*3.0-rot*3.0-rot)
				flicker := 0.66 + 0.34*math.Sin(float64(v.frame)*0.5+t*6.0+m.mid*10.0)
				bright := doppler*flicker*(0.45+0.55*filament) + m.high*0.28

				innerFrac := 1.0 - (r-kCore)/(kDiskE-kCore) // 1 at hole, 0 at edge
				tag := 0
				switch {
				case bright > 0.72:
					tag = 2
				case bright > 0.44:
					tag = 1
				}
				if innerFrac > 0.5 && tag < 2 && bright > 0.52 {
					tag = 2
				}
				if innerFrac < 0.18 && tag == 2 {
					tag = 1
				}
				setDot(x, y, tag)
				continue
			}

			// --- Gravitational pulse ring (elliptical, crosses the window). ---
			if d.shockStr > 0.01 {
				if math.Abs(r-d.shockR) < 0.6+d.shockStr {
					fade := 1.0 - math.Abs(r-d.shockR)/(0.6+d.shockStr)
					if fade > 0.28 {
						setDot(x, y, 2)
					}
				}
			}
		}
	}

	d.renderInfall(v, m, g, setDot)
	d.renderPulseOrbs(v, m, g, setDot)
	d.renderOrbs(v, m, g, setDot)
	d.renderNovas(v, m, g, setDot)

	// --- Compose into Braille lines with tier-color batching. ---
	lines := make([]string, height)
	for row := 0; row < height; row++ {
		var sb, run strings.Builder
		tag := -1
		for col := 0; col < PanelWidth; col++ {
			cellT := int(cellTag[row*PanelWidth+col])
			var braille rune = '\u2800'
			for dr := 0; dr < 4; dr++ {
				for dc := 0; dc < 2; dc++ {
					if lit[(row*4+dr)*dotCols+col*2+dc] {
						braille |= brailleBit[dr][dc]
					}
				}
			}
			if cellT != tag {
				flushStyleRun(&sb, &run, tag)
				tag = cellT
			}
			run.WriteRune(braille)
		}
		flushStyleRun(&sb, &run, tag)
		lines[row] = sb.String()
	}
	return strings.Join(lines, "\n")
}

// renderInfall draws matter spiralling into the hole. The infall speed scales
// with the bass so deep hits visibly accelerate the streams, and each particle
// brightens as it approaches the event horizon (accretion heating).
func (d *voidDriver) renderInfall(v *Visualizer, m voidMetrics, g voidGeom, setDot func(int, int, int)) {
	const streams = 26
	speed := 0.012 + m.bass*0.06
	swirl := 2.0 + m.mid*3.0

	for i := 0; i < streams; i++ {
		seed := uint64(i)*104729 + 7919
		p := math.Mod(float64(d.infallPhase)*speed+float64(i%7), 1.0)
		prog := p // 0 (outer) → 1 (horizon)
		r := 1.0 - prog*(1.0-g.kCore)*0.98
		t := float64((seed%628))/100.0 + prog*swirl + math.Sin(float64(d.infallPhase)*0.1+float64(i))*0.15
		dx := r * g.aX * math.Cos(t)
		dy := r * g.aY * math.Sin(t)

		x := int(g.mx + dx)
		y := int(g.my + g.sway + dy)
		heat := prog
		tag := 1
		if heat > 0.62 {
			tag = 2
		} else if heat < 0.3 {
			tag = 0
		}
		if rand01(seed+uint64(d.infallPhase/8)) < 0.22 {
			continue
		}
		setDot(x, y, tag)
		setDot(x+1, y, tag)
	}
}

// renderPulseOrbs draws the gravitational pulse as a ring of discrete particles
// — shrapnel flying outward from every bass hit. They ride the expanding ring
// all the way across the window, so each kick visibly launches a wave of orbs.
func (d *voidDriver) renderPulseOrbs(v *Visualizer, m voidMetrics, g voidGeom, setDot func(int, int, int)) {
	if d.shockStr <= 0.01 {
		return
	}
	// Number of particles scales with how strong the pulse's kick was.
	n := 24 + int(d.shockStr*40)
	for i := 0; i < n; i++ {
		theta := (float64(i) / float64(n)) * 2 * math.Pi
		// Angular jitter so the ring reads as discrete orbs, not a smooth band.
		theta += (rand01(uint64(i)+d.infallPhase) - 0.5) * 0.12
		// Radial jitter keeps each orb inside the band.
		rr := d.shockR + (rand01(uint64(i)*3+d.infallPhase)-0.5)*(0.6+d.shockStr)

		px := rr * g.aX * math.Cos(theta)
		py := rr * g.aY * math.Sin(theta)
		x := int(g.mx + px)
		y := int(g.my + g.sway + py)
		if x < 0 || x >= g.dotCols || y < 0 || y >= g.dotRows {
			continue
		}
		// Hot at launch, cooling as the ring expands.
		tier := 2
		if rr > 1.6 {
			tier = 1
		}
		if rr > 2.4 {
			tier = 0
		}
		setDot(x, y, tier)
		if rand01(uint64(i)*7+d.infallPhase) > 0.5 {
			setDot(x+1, y, tier)
		}
	}
}

// renderOrbs draws the ejected orbs flying outward from the black hole. Each orb
// departs the disk edge and travels outward across the whole window, leaving a
// short trailing tail, cooled (red→yellow→green) and flickered by the frequency
// bands along its path.
func (d *voidDriver) renderOrbs(v *Visualizer, m voidMetrics, g voidGeom, setDot func(int, int, int)) {
	alive := d.orbs[:0]
	for _, o := range d.orbs {
		age := int64(v.frame) - int64(o.birth)
		if age >= orbLifetime {
			continue
		}
		alive = append(alive, o)

		t := float64(age) * o.speed // normalized travel distance
		r := 1.0 + t                // start at disk edge, fly outward
		cx := g.mx + r*g.aX*math.Cos(o.angle)
		cy := g.my + g.sway + r*g.aY*math.Sin(o.angle)

		// Orb cools as it travels and flickers with the treble, but always stays
		// bright enough to read as a shooting projectile across the window.
		tier := 1
		switch {
		case t < 0.3:
			tier = 2
		case t < 0.55:
			tier = 1
		}
		if rand01(o.seed+uint64(v.frame)) < 0.45+m.high*0.5 {
			tier = 2 // treble glint
		}

		// Bright 2x2 head plus a long fading tail behind it so the outward
		// flight reads clearly even against the disk structure.
		hi := tier
		lo := 0
		if hi > 1 {
			lo = 1
		}
		setDot(int(cx), int(cy), hi)
		setDot(int(cx)+1, int(cy), hi)
		setDot(int(cx), int(cy)+1, lo)
		setDot(int(cx)+1, int(cy)+1, lo)
		dx := math.Cos(o.angle)
		dy := math.Sin(o.angle)
		for k := 1; k <= 6; k++ {
			tx := cx - dx*float64(k)*1.4
			ty := cy - dy*float64(k)*1.4
			tt := tier - 1
			if tt < 0 {
				tt = 0
			}
			if k > 3 {
				tt = 0
			}
			setDot(int(tx), int(ty), tt)
		}
	}
	d.orbs = alive
}

// renderNovas draws distant stellar explosions. Each flares, expands into a
// fading ring of embers, and is coloured by a red→yellow→green progression as
// it cools. They only happen on strong transients (see Tick).
func (d *voidDriver) renderNovas(v *Visualizer, m voidMetrics, g voidGeom, setDot func(int, int, int)) {
	alive := d.novas[:0]
	for _, n := range d.novas {
		age := int64(v.frame) - int64(n.birth)
		if age >= novaLifetime {
			continue
		}
		alive = append(alive, n)

		t := float64(age) / novaLifetime // 0..1
		cx := g.mx + n.bx*g.aX
		cy := g.my + g.sway + n.by*g.aY
		expand := 1.0 + t*7.0
		fade := 1.0 - t

		if t < 0.3 {
			coreTag := 2
			if t > 0.18 {
				coreTag = 1
			}
			setDot(int(cx), int(cy), coreTag)
			setDot(int(cx)+1, int(cy), coreTag)
			setDot(int(cx), int(cy)+1, coreTag)
		}

		bbox := int(expand) + 2
		for dy := -bbox; dy <= bbox; dy++ {
			for dx := -bbox; dx <= bbox; dx++ {
				dist := math.Hypot(float64(dx), float64(dy))
				if math.Abs(dist-expand) > 0.8 {
					continue
				}
				if rand01(uint64(age*13)+uint64(dx)*31+uint64(dy)*17) > fade*0.9 {
					continue
				}
				tag := 2
				if t > 0.45 {
					tag = 1
				}
				if t > 0.7 {
					tag = 0
				}
				setDot(int(cx)+dx, int(cy)+dy, tag)
			}
		}
	}
	d.novas = alive
}
