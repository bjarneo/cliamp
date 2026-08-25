package ui

import "time"

// geyserDriver draws a particle fountain rooted at the bottom of the panel.
// Sustained loudness keeps a steady column of mist, bass transients launch
// strong vertical jets, and every particle then arcs back down under gravity
// with a touch of lateral spray. Particles inherit a tier from the band that
// produced them, so dense bass passages paint the column red and treble
// embellishments add green sparkles to the canopy.
type geyserDriver struct {
	grid      brailleGrid
	particles []geyserParticle
	rng       uint64
	prevBass  float64
}

type geyserParticle struct {
	x, y   float64
	vx, vy float64
	tier   int8
	life   int
}

func newGeyserDriver() visModeDriver { return &geyserDriver{rng: 0xFEED5EED} }

func (*geyserDriver) AnalysisSpec(*Visualizer) VisAnalysisSpec {
	return spectrumAnalysisSpec(DefaultSpectrumBands)
}

func (d *geyserDriver) Tick(v *Visualizer, ctx VisTickContext) {
	defaultDriverTick(v, ctx, d.AnalysisSpec(v))
	if ctx.OverlayActive {
		return
	}
	dotRows, dotCols := v.Rows*4, PanelWidth*2
	if dotRows < 4 || dotCols < 4 {
		return
	}
	d.grid.ensure(dotRows, dotCols)
	d.grid.clear()

	bands := v.SmoothedBands()
	if len(bands) == 0 {
		return
	}
	bass := bandAvg(bands, 0, max(1, len(bands)/3))
	mid := bandAvg(bands, len(bands)/3, 2*len(bands)/3)
	high := bandAvg(bands, 2*len(bands)/3, len(bands))
	delta := bass - d.prevBass
	d.prevBass = bass

	jetX := dotCols / 2
	jetSpread := max(2, dotCols/16)

	// Steady drizzle: spawn rate scales with overall loudness so quiet passages
	// idle a thin trickle and loud passages keep a column going. Bass weights
	// most heavily so a heavy bassline alone keeps the column flowing.
	steady := bass*0.85 + mid*0.25 + high*0.08
	for i := 0; i < int(steady*6); i++ {
		d.spawn(jetX, dotRows-1, jetSpread, 1.5+steady*4.5, &bass, &mid, &high)
	}

	// Transient kick: shoot a thick burst. Triggers on smaller deltas now so
	// even gentler kick drums register.
	if delta > 0.06 && bass > 0.15 {
		burst := 40 + int(delta*180)
		for i := 0; i < burst; i++ {
			d.spawn(jetX, dotRows-1, jetSpread*2, 4.5+delta*10.0+bass*4.0, &bass, &mid, &high)
		}
	}

	// Advance particles.
	const gravity = 0.30
	const drag = 0.992
	live := d.particles[:0]
	for _, p := range d.particles {
		p.vy += gravity
		p.vx *= drag
		p.x += p.vx
		p.y += p.vy
		p.life++
		ix, iy := int(p.x), int(p.y)
		if iy >= dotRows || ix < 0 || ix >= dotCols || p.life > 200 {
			continue
		}
		if iy < 0 {
			iy = 0
		}
		d.grid.set(ix, iy, p.tier)
		live = append(live, p)
	}
	d.particles = live
}

func (d *geyserDriver) spawn(x, y, spread int, vy float64, bass, mid, high *float64) {
	jx := x + int(rng64(&d.rng)*float64(2*spread+1)) - spread
	vyJitter := vy * (0.6 + rng64(&d.rng)*0.5)
	vxJitter := (rng64(&d.rng) - 0.5) * (1.0 + vy*0.4)
	r := rng64(&d.rng)
	var tier int8 = 1
	switch {
	case r < *bass:
		tier = 3
	case r < *bass+*mid:
		tier = 2
	default:
		_ = high
	}
	d.particles = append(d.particles, geyserParticle{
		x: float64(jx), y: float64(y),
		vx: vxJitter, vy: -vyJitter,
		tier: tier,
	})
}

func (*geyserDriver) TickInterval(_ *Visualizer, ctx VisTickContext) time.Duration {
	return defaultDriverTickInterval(ctx)
}
func (d *geyserDriver) pauseSettled() bool { return len(d.particles) == 0 }
func (d *geyserDriver) OnEnter(*Visualizer) {
	d.grid = brailleGrid{}
	d.particles = nil
	d.prevBass = 0
}
func (*geyserDriver) OnLeave(*Visualizer) {}
func (d *geyserDriver) Render(v *Visualizer) string {
	return d.grid.render(v.Rows)
}
