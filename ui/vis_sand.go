package ui

import "strings"

// sandDriver runs a falling-sand cellular automaton on the dot grid. Each
// frame, new grains drop from the top — colored by which spectrum band
// triggered them — and existing grains fall straight down or, if blocked,
// slide diagonally onto piles. Bass adds a small "shake" by occasionally
// nudging a row sideways, which destabilises slopes and triggers little
// avalanches; loud passages keep the panel actively pouring.
type sandDriver struct {
	spectrumDriverBase

	grid     brailleGrid // tiers 1, 2 and 3 are green, yellow and red
	rng      uint64
	prevBass float64 // for detecting bass transients that bump the bed

	// Explosion phase: when triggered, all grains become ballistic particles
	// for a few dozen frames. While particles exist, normal spawning, bumping,
	// and falling are suspended; the grid is re-derived from particle positions
	// each tick so the existing renderer needs no changes.
	particles    []sandParticle
	explosionTTL int
}

// sandParticle is one grain in mid-flight during the explosion sequence. Sub-
// dot positions and a real velocity per particle let us see the rise, peak,
// scatter, and fall across many frames instead of a single teleport.
type sandParticle struct {
	x, y   float64
	vx, vy float64
	tier   int8
}

func newSandDriver() visModeDriver {
	return &sandDriver{rng: 0x5A4D5A4D5A4D}
}

func (d *sandDriver) Tick(v *Visualizer, ctx VisTickContext) {
	defaultDriverTick(v, ctx, d.AnalysisSpec(v))
	if ctx.OverlayActive {
		return
	}
	dotRows := v.Rows * 4
	dotCols := v.columns() * 2
	if dotRows < 4 || dotCols < 4 {
		return
	}
	d.grid.resize(dotRows, dotCols)
	grid := d.grid.cells

	bands := v.SmoothedBands()
	bass := bandAvg(bands, 0, max(1, len(bands)/3))
	bandCount := len(bands)

	// EXPLOSION PHASE: while particles are still in flight we suspend the
	// normal sand simulation entirely and just animate the burst. The grid is
	// re-derived from particle positions each tick so the renderer is
	// unchanged.
	if d.explosionTTL > 0 || len(d.particles) > 0 {
		d.tickExplosion()
		d.prevBass = bass
		return
	}

	// Spawn grains: each band emits at a column proportional to its index, with
	// a small spread so neighbouring grains don't stack into a single tower.
	if bandCount > 0 {
		for b := 0; b < bandCount; b++ {
			level := bands[b]
			if level < 0.10 {
				continue
			}
			// Probability of emitting this frame scales with band level.
			if rng64(&d.rng) > level*0.85 {
				continue
			}
			centre := (b*2 + 1) * dotCols / (2 * bandCount)
			spread := dotCols / (bandCount * 2)
			if spread < 1 {
				spread = 1
			}
			x := centre + int(rng64(&d.rng)*float64(2*spread)) - spread
			if x < 0 {
				x = 0
			}
			if x >= dotCols {
				x = dotCols - 1
			}
			// Tier mapping: low bands → red (hot bass), mid → yellow, high → green.
			var tier int8 = 1
			switch {
			case b < bandCount/3:
				tier = 3 // red
			case b < 2*bandCount/3:
				tier = 2 // yellow
			default:
				tier = 1 // green
			}
			if grid[0*dotCols+x] == 0 {
				grid[0*dotCols+x] = tier
			}
		}
	}

	// Bass-driven bumps. Three regimes layered together:
	//
	//   0. EXPLOSION — when the bed has accumulated past ~40% capacity, the
	//      next bass kick blows everything sky-high. Grains are launched far
	//      enough that most fly off the top of the panel and disappear; the
	//      simulation then starts over from an empty bed.
	//   1. TRANSIENT BUMP — rising-edge of bass, fires once per kick. This is
	//      the speaker-cone slap: violent vertical lift across the WHOLE bed,
	//      with grains thrown high and far. Closer to the bottom = more lift,
	//      but every grain has a chance to fly.
	//   2. SUSTAINED RUMBLE — when bass stays high, every frame jitters
	//      grains a small amount so the bed never settles still during a
	//      heavy bass passage. This is what makes the sand keep dancing on
	//      sustained kicks instead of just popping once and freezing.
	delta := bass - d.prevBass
	d.prevBass = bass

	// 0. Explosion check: fires before the normal bump branches so the
	// grid is cleared *instead* of being merely shaken when overfilled.
	if delta > 0.06 && bass > 0.15 {
		fill := 0
		for _, g := range grid {
			if g != 0 {
				fill++
			}
		}
		if float64(fill)/float64(len(grid)) > 0.30 {
			// Convert every grain into a ballistic particle and enter the
			// explosion phase. The simulation will animate the burst over
			// the next few dozen frames, then resume.
			d.startExplosion()
			return
		}
	}

	// 1. Transient bump.
	if delta > 0.06 && bass > 0.15 {
		strength := delta*3.5 + bass*0.8
		if strength > 1.4 {
			strength = 1.4
		}
		// Process top-down so a lifted grain isn't visited again this frame.
		for y := 0; y < dotRows; y++ {
			depthFrac := float64(y) / float64(max(1, dotRows-1)) // 0 at top, 1 at bottom
			// Probability close to 1 near the bottom on a strong kick — the bed
			// effectively detonates upward.
			liftProb := strength * (0.30 + 0.70*depthFrac)
			if liftProb > 0.95 {
				liftProb = 0.95
			}
			// Lift height scales with strength AND depth — a 1.0-strength kick
			// can throw a bottom grain ~10 dot rows (half the panel). Multiply
			// by ~7 to feel like a speaker cone, not a soft tap.
			liftMax := 2 + int(strength*7.0*(0.4+0.6*depthFrac))
			// Lateral spread also scales — sand sprays out, not just up.
			jitterRange := 1 + int(strength*5.0)
			for x := 0; x < dotCols; x++ {
				g := grid[y*dotCols+x]
				if g == 0 {
					continue
				}
				if rng64(&d.rng) > liftProb {
					continue
				}
				lift := 1 + int(rng64(&d.rng)*float64(liftMax))
				jitter := int(rng64(&d.rng)*float64(2*jitterRange+1)) - jitterRange
				ny := y - lift
				nx := x + jitter
				if ny < 0 {
					ny = 0
				}
				if nx < 0 {
					nx = 0
				}
				if nx >= dotCols {
					nx = dotCols - 1
				}
				if grid[ny*dotCols+nx] == 0 {
					grid[ny*dotCols+nx] = g
					grid[y*dotCols+x] = 0
				}
			}
		}
	}

	// 2. Sustained rumble — applies whenever bass is high, regardless of
	// transient. Smaller per-grain motion but applied every frame, so the bed
	// keeps churning during a held kick.
	if bass > 0.30 {
		// Strength climbs with how far above the threshold we are.
		rumble := (bass - 0.30) * 1.8
		if rumble > 0.6 {
			rumble = 0.6
		}
		// Only churn the bottom half — that's what's coupled to the speaker.
		minY := dotRows / 2
		for y := minY; y < dotRows; y++ {
			depthFrac := float64(y-minY) / float64(max(1, dotRows-1-minY))
			prob := rumble * (0.15 + 0.55*depthFrac)
			for x := 0; x < dotCols; x++ {
				g := grid[y*dotCols+x]
				if g == 0 {
					continue
				}
				if rng64(&d.rng) > prob {
					continue
				}
				lift := 1 + int(rng64(&d.rng)*2.0) // 1..2
				jitter := int(rng64(&d.rng)*5) - 2 // -2..+2
				ny := y - lift
				nx := x + jitter
				if ny < 0 {
					ny = 0
				}
				if nx < 0 {
					nx = 0
				}
				if nx >= dotCols {
					nx = dotCols - 1
				}
				if grid[ny*dotCols+nx] == 0 {
					grid[ny*dotCols+nx] = g
					grid[y*dotCols+x] = 0
				}
			}
		}
	}

	// Falling pass: bottom-up so a grain we just moved into y+1 isn't moved
	// twice this frame. Grains at the bottom row leave the grid.
	for y := dotRows - 2; y >= 0; y-- {
		// Alternate horizontal scan direction each frame so piles don't lean
		// permanently to one side from diagonal-left-first bias.
		leftFirst := (v.frame % 2) == 0
		startX, endX, stepX := 0, dotCols, 1
		if !leftFirst {
			startX, endX, stepX = dotCols-1, -1, -1
		}
		for x := startX; x != endX; x += stepX {
			g := grid[y*dotCols+x]
			if g == 0 {
				continue
			}
			// Try straight down.
			if grid[(y+1)*dotCols+x] == 0 {
				grid[(y+1)*dotCols+x] = g
				grid[y*dotCols+x] = 0
				continue
			}
			// Diagonal: pick left or right first based on parity for symmetry.
			diag1, diag2 := -1, 1
			if rng64(&d.rng) < 0.5 {
				diag1, diag2 = 1, -1
			}
			for _, dx := range [2]int{diag1, diag2} {
				nx := x + dx
				if nx < 0 || nx >= dotCols {
					continue
				}
				if grid[(y+1)*dotCols+nx] == 0 {
					grid[(y+1)*dotCols+nx] = g
					grid[y*dotCols+x] = 0
					break
				}
			}
		}
	}

	// Floor: grains in the very bottom row drift off-screen at a slow rate so
	// the grid doesn't fill up over time. Without this, a long-running session
	// gradually packs every cell.
	for x := 0; x < dotCols; x++ {
		if grid[(dotRows-1)*dotCols+x] != 0 && rng64(&d.rng) < 0.04 {
			grid[(dotRows-1)*dotCols+x] = 0
		}
	}
}

func (d *sandDriver) pauseSettled() bool {
	return d.explosionTTL == 0 && len(d.particles) == 0
}

func (d *sandDriver) OnEnter(*Visualizer) {
	d.grid = brailleGrid{}
	d.prevBass = 0
	d.particles = nil
	d.explosionTTL = 0
}

// startExplosion converts every grain on the grid into a ballistic particle
// with a random outward velocity, then enters the multi-frame explosion
// phase. Bottom grains carry slightly more upward energy (they're closer to
// the speaker cone), so the burst peaks naturally from below.
func (d *sandDriver) startExplosion() {
	dotRows, dotCols := d.grid.dotRows, d.grid.dotCols
	grid := d.grid.cells
	d.particles = d.particles[:0]
	for y := 0; y < dotRows; y++ {
		depthFrac := float64(y) / float64(max(1, dotRows-1)) // 0=top, 1=bottom
		for x := 0; x < dotCols; x++ {
			g := grid[y*dotCols+x]
			if g == 0 {
				continue
			}
			grid[y*dotCols+x] = 0
			// Vertical: -3..-9 dot/frame upward, biased so bottom grains fly
			// fastest. Lateral: ±4 dot/frame for a wide spray.
			vy := -(2.0 + rng64(&d.rng)*5.0 + depthFrac*2.0)
			vx := (rng64(&d.rng) - 0.5) * 8.0
			d.particles = append(d.particles, sandParticle{
				x:    float64(x),
				y:    float64(y),
				vx:   vx,
				vy:   vy,
				tier: g,
			})
		}
	}
	// Generous TTL — particles will mostly fall off earlier; the natural end
	// is when the particles list empties. TTL is the safety cap.
	d.explosionTTL = 80
}

// tickExplosion advances all in-flight particles one frame: gravity pulls
// them down, drag slows lateral motion, and any particle that leaves the
// panel through any edge is removed. The grid is fully rebuilt from the
// surviving particles so the renderer can stay unchanged.
func (d *sandDriver) tickExplosion() {
	const gravity = 0.50
	const drag = 0.985
	dotRows, dotCols := d.grid.dotRows, d.grid.dotCols
	grid := d.grid.cells

	d.grid.clear()

	live := d.particles[:0]
	for _, p := range d.particles {
		p.vy += gravity
		p.vx *= drag
		p.x += p.vx
		p.y += p.vy
		ix := int(p.x)
		iy := int(p.y)
		if iy < 0 || iy >= dotRows || ix < 0 || ix >= dotCols {
			// Off panel — particle is gone (continued ballistic flight beyond
			// our viewport doesn't matter visually).
			continue
		}
		grid[iy*dotCols+ix] = p.tier
		live = append(live, p)
	}
	d.particles = live

	if d.explosionTTL > 0 {
		d.explosionTTL--
	}
	if len(d.particles) == 0 {
		d.explosionTTL = 0
	}
}

func (d *sandDriver) Render(v *Visualizer) string {
	height := v.Rows
	dotRows := height * 4
	dotCols := v.columns() * 2
	if dotRows < 4 || dotCols < 4 {
		return strings.Repeat("\n", max(0, height-1))
	}
	d.grid.resize(dotRows, dotCols)
	return d.grid.render(height, v.columns())
}
