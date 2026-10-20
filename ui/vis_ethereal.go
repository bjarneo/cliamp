package ui

import (
	"math"
	"strings"
	"time"

	"charm.land/lipgloss/v2"
)

// Ethereal follows Omarchy's Ethereal cosmic wallpaper: midnight blue clouds,
// violet rims and peach starlight. Its own palette keeps that identity under
// any UI theme; no installed Omarchy files are needed at runtime.
var etherealColors = [...]string{
	"#304577", "#6d7db6", "#7d82d9", "#c89dc1", "#ffcead", "#a3bfd1", "#dfeaf0",
}

const (
	etherealStarDim  = 2
	etherealStarCool = 6
	etherealStarLit  = 7
)

// Only foreground dots are painted, like Flame; the terminal background stays
// untouched. Unlike the theme-derived spectrum styles these colours never change.
var etherealANSI [len(etherealColors)]styleANSI

func init() {
	for i, c := range etherealColors {
		etherealANSI[i] = foregroundANSI(lipgloss.Color(c))
	}
}

func flushEtherealRun(sb, run *strings.Builder, tier int) {
	var style styleANSI
	if tier > 0 && tier <= len(etherealANSI) {
		style = etherealANSI[tier-1]
	}
	writeStyledRun(sb, run, style)
}

type etherealStar struct {
	x, y         int
	phase, speed float64
}

type etherealDriver struct {
	spectrumDriverBase
	cols, rows int
	grid       brailleGrid
	stars      []etherealStar
	elapsed    float64
	lastTick   time.Time
}

func newEtherealDriver() visModeDriver { return &etherealDriver{} }

func (d *etherealDriver) OnEnter(*Visualizer) {
	d.elapsed, d.lastTick = 0, time.Time{}
}

func (d *etherealDriver) Tick(v *Visualizer, ctx VisTickContext) {
	defaultDriverTick(v, ctx, d.AnalysisSpec(v))
	if ctx.OverlayActive || !ctx.Playing || ctx.Paused {
		d.lastTick = time.Time{}
		return
	}
	if !ctx.Now.IsZero() && ctx.Now.Equal(d.lastTick) {
		return
	}
	d.elapsed += clampFrameDT(ctx.Now, d.lastTick, TickFast).Seconds()
	d.lastTick = ctx.Now
}

func (d *etherealDriver) layout(cols, rows int) {
	if cols == d.cols && rows == d.rows {
		return
	}
	d.cols, d.rows = cols, rows
	d.stars = d.stars[:0]
	seed := uint64(0x455448455245414c)
	for i := range cols * rows {
		if rng64(&seed) < 0.035 {
			d.stars = append(d.stars, etherealStar{
				x: i%cols*2 + int(lcgNext(&seed)%2), y: i/cols*4 + int(lcgNext(&seed)%4),
				phase: rng64(&seed) * 2 * math.Pi, speed: 0.4 + rng64(&seed)*0.7,
			})
		}
	}
}

// etherealCloud samples a slowly warped band of nebula. Noise supplies both
// shadowed bodies and thin luminous rims, rather than a stack of sine waves.
// Bass opens the cloud; mids lift its light. The existing deterministic value
// noise also used by Omarchy keeps the texture continuous between frames.
func etherealCloud(x, y, t, bass, mid float64) float64 {
	u, v := x*6.8, y*3.2
	warp := omarchyNoiseAt(u*0.7+t*0.09, v*0.8-t*0.06)
	noise := 0.60*omarchyNoiseAt(u+warp*1.8-t*0.12, v+t*0.08) +
		0.28*omarchyNoiseAt(u*2.3+t*0.07, v*2.3-warp) +
		0.12*omarchyNoiseAt(u*5.1-t*0.05, v*5.1+t*0.11)
	centre := 0.53 + 0.16*math.Sin(x*6.2+t*0.13) + 0.06*math.Sin(x*13-t*0.09)
	distance := math.Abs(y - centre + (warp-0.5)*0.3)
	cloud := max(0, 1-distance/(0.18+0.13*bass))
	halo := max(0, 1-distance/0.5)
	body := max(0, (noise-0.30)*1.4)
	rim := max(0, 1-math.Abs(noise-0.53)*13)
	light := halo*(0.06+body*0.25) + cloud*(body*0.38+rim*(0.42+0.30*bass+0.30*mid))
	return min(1, max(0, light))
}

func (d *etherealDriver) Render(v *Visualizer) string {
	rows, cols := v.Rows, v.columns()
	if rows <= 0 || cols <= 0 {
		return strings.Repeat("\n", max(0, rows-1))
	}
	d.layout(cols, rows)
	bands := v.SmoothedBands()
	n := len(bands)
	// Non-empty ranges let a short analysis array drive every part too.
	bass := min(1, max(0, bandAvg(bands, 0, max(1, n/3))))
	mid := min(1, max(0, bandAvg(bands, n/3, max(1, 2*n/3))))
	high := min(1, max(0, bandAvg(bands, 2*n/3, n)))
	t := d.elapsed
	dotRows, dotCols := rows*4, cols*2
	d.grid.flush, d.grid.keepRunOnEmpty = flushEtherealRun, true
	d.grid.ensure(dotRows, dotCols)
	for y := range dotRows {
		for x := range dotCols {
			light := etherealCloud((float64(x)+0.5)/float64(dotCols), float64(y)/float64(dotRows), t, bass, mid)
			// A fixed threshold per dot makes wispy edges dissolve locally,
			// without re-randomizing the entire cloud on every frame.
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
			d.grid.set(x, y, tier)
		}
	}
	for _, star := range d.stars {
		glow := 0.5 + 0.5*math.Sin(t*star.speed+star.phase)
		if glow+high*0.35 <= 0.36 {
			continue
		}
		tier := int8(etherealStarDim)
		if glow+high*0.25 > 0.78 {
			tier = etherealStarCool
		}
		if glow+high*0.15 > 1.02 {
			tier = etherealStarLit
			for _, offset := range [4][2]int{{-1, 0}, {1, 0}, {0, -1}, {0, 1}} {
				d.grid.set(star.x+offset[0], star.y+offset[1], tier)
			}
		}
		d.grid.set(star.x, star.y, tier)
	}
	return d.grid.render(rows, cols)
}
