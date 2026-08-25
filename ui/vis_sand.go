package ui

import (
	"strings"
	"time"
)

// sandDriver runs a falling-sand cellular automaton on the dot grid. Each
// frame, new grains drop from the top — colored by which spectrum band
// triggered them — and existing grains fall straight down or, if blocked,
// slide diagonally onto piles. Bass adds a small "shake" by occasionally
// nudging a row sideways, which destabilises slopes and triggers little
// avalanches; loud passages keep the panel actively pouring.
type sandDriver struct {
	grid             []int8 // 0 = empty; 1 = green tier; 2 = yellow; 3 = red
	dotRows, dotCols int
	rng              uint64
	prevBass         float64 // for detecting bass transients that bump the bed

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

func (*sandDriver) AnalysisSpec(*Visualizer) VisAnalysisSpec {
	return spectrumAnalysisSpec(DefaultSpectrumBands)
}

func (d *sandDriver) ensure(rows, cols int) {
	if rows == d.dotRows && cols == d.dotCols && len(d.grid) == rows*cols {
		return
	}
	d.grid = make([]int8, rows*cols)
	d.dotRows = rows
	d.dotCols = cols
}

// rand01 returns a deterministic pseudo-random float in [0,1).
func (d *sandDriver) rand01() float64 {
	d.rng = d.rng*6364136223846793005 + 1442695040888963407
	return float64((d.rng>>33)%1000) / 1000.0
}

func (d *sandDriver) Tick(v *Visualizer, ctx VisTickContext) {
	defaultDriverTick(v, ctx, d.AnalysisSpec(v))
	if ctx.OverlayActive {
		return
	}
	dotRows := v.Rows * 4
	dotCols := PanelWidth * 2
	if dotRows < 4 || dotCols < 4 {
		return
	}
	d.ensure(dotRows, dotCols)

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
			if d.rand01() > level*0.85 {
				continue
			}
			centre := (b*2 + 1) * dotCols / (2 * bandCount)
			spread := dotCols / (bandCount * 2)
			if spread < 1 {
				spread = 1
			}
			x := centre + int(d.rand01()*float64(2*spread)) - spread
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
			if d.grid[0*dotCols+x] == 0 {
				d.grid[0*dotCols+x] = tier
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
		for _, g := range d.grid {
			if g != 0 {
				fill++
			}
		}
		if float64(fill)/float64(len(d.grid)) > 0.30 {
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
				g := d.grid[y*dotCols+x]
				if g == 0 {
					continue
				}
				if d.rand01() > liftProb {
					continue
				}
				lift := 1 + int(d.rand01()*float64(liftMax))
				jitter := int(d.rand01()*float64(2*jitterRange+1)) - jitterRange
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
				if d.grid[ny*dotCols+nx] == 0 {
					d.grid[ny*dotCols+nx] = g
					d.grid[y*dotCols+x] = 0
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
				g := d.grid[y*dotCols+x]
				if g == 0 {
					continue
				}
				if d.rand01() > prob {
					continue
				}
				lift := 1 + int(d.rand01()*2.0) // 1..2
				jitter := int(d.rand01()*5) - 2 // -2..+2
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
				if d.grid[ny*dotCols+nx] == 0 {
					d.grid[ny*dotCols+nx] = g
					d.grid[y*dotCols+x] = 0
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
			g := d.grid[y*dotCols+x]
			if g == 0 {
				continue
			}
			// Try straight down.
			if d.grid[(y+1)*dotCols+x] == 0 {
				d.grid[(y+1)*dotCols+x] = g
				d.grid[y*dotCols+x] = 0
				continue
			}
			// Diagonal: pick left or right first based on parity for symmetry.
			diag1, diag2 := -1, 1
			if d.rand01() < 0.5 {
				diag1, diag2 = 1, -1
			}
			for _, dx := range [2]int{diag1, diag2} {
				nx := x + dx
				if nx < 0 || nx >= dotCols {
					continue
				}
				if d.grid[(y+1)*dotCols+nx] == 0 {
					d.grid[(y+1)*dotCols+nx] = g
					d.grid[y*dotCols+x] = 0
					break
				}
			}
		}
	}

	// Floor: grains in the very bottom row drift off-screen at a slow rate so
	// the grid doesn't fill up over time. Without this, a long-running session
	// gradually packs every cell.
	for x := 0; x < dotCols; x++ {
		if d.grid[(dotRows-1)*dotCols+x] != 0 && d.rand01() < 0.04 {
			d.grid[(dotRows-1)*dotCols+x] = 0
		}
	}
}

func (*sandDriver) TickInterval(_ *Visualizer, ctx VisTickContext) time.Duration {
	return defaultDriverTickInterval(ctx)
}

func (d *sandDriver) pauseSettled() bool {
	return d.explosionTTL == 0 && len(d.particles) == 0
}

func (d *sandDriver) OnEnter(*Visualizer) {
	d.grid = nil
	d.dotRows = 0
	d.dotCols = 0
	d.prevBass = 0
	d.particles = nil
	d.explosionTTL = 0
}

func (*sandDriver) OnLeave(*Visualizer) {}

// startExplosion converts every grain on the grid into a ballistic particle
// with a random outward velocity, then enters the multi-frame explosion
// phase. Bottom grains carry slightly more upward energy (they're closer to
// the speaker cone), so the burst peaks naturally from below.
func (d *sandDriver) startExplosion() {
	dotRows, dotCols := d.dotRows, d.dotCols
	d.particles = d.particles[:0]
	for y := 0; y < dotRows; y++ {
		depthFrac := float64(y) / float64(max(1, dotRows-1)) // 0=top, 1=bottom
		for x := 0; x < dotCols; x++ {
			g := d.grid[y*dotCols+x]
			if g == 0 {
				continue
			}
			d.grid[y*dotCols+x] = 0
			// Vertical: -3..-9 dot/frame upward, biased so bottom grains fly
			// fastest. Lateral: ±4 dot/frame for a wide spray.
			vy := -(2.0 + d.rand01()*5.0 + depthFrac*2.0)
			vx := (d.rand01() - 0.5) * 8.0
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
	dotRows, dotCols := d.dotRows, d.dotCols

	for i := range d.grid {
		d.grid[i] = 0
	}

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
		d.grid[iy*dotCols+ix] = p.tier
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
	dotCols := PanelWidth * 2
	if dotRows < 4 || dotCols < 4 {
		return strings.Repeat("\n", max(0, height-1))
	}
	if d.dotRows != dotRows || d.dotCols != dotCols || len(d.grid) != dotRows*dotCols {
		d.ensure(dotRows, dotCols)
	}

	lines := make([]string, height)
	for row := 0; row < height; row++ {
		var sb, run strings.Builder
		tag := -1
		for col := 0; col < PanelWidth; col++ {
			var braille rune = '⠀'
			cellTag := -1
			for dr := 0; dr < 4; dr++ {
				for dc := 0; dc < 2; dc++ {
					y := row*4 + dr
					x := col*2 + dc
					g := d.grid[y*dotCols+x]
					if g == 0 {
						continue
					}
					// Tier: 1=green(0), 2=yellow(1), 3=red(2)
					t := int(g) - 1
					braille |= brailleBit[dr][dc]
					if t > cellTag {
						cellTag = t
					}
				}
			}
			if cellTag < 0 {
				cellTag = 0
			}
			if cellTag != tag {
				flushStyleRun(&sb, &run, tag)
				tag = cellTag
			}
			run.WriteRune(braille)
		}
		flushStyleRun(&sb, &run, tag)
		lines[row] = sb.String()
	}
	return strings.Join(lines, "\n")
}
