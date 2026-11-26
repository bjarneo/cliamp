package ui

import (
	"image/color"
	"math"
	"strings"
	"time"
)

// Ethereal follows Omarchy's cosmic wallpaper with drifting clouds and stars.
const (
	etherealStarDim  = 2
	etherealStarCool = 6
	etherealStarLit  = 7
)

// Only foreground dots are painted, like Flame; the terminal background stays
// untouched. ApplyThemeColors rebuilds the styles from the shared palette.
var etherealANSI [etherealStarLit]styleANSI

func refreshEtherealANSI() {
	colors := [etherealStarLit]color.Color{
		ColorDim, SpectrumLow, SpectrumMid, SpectrumHigh, ColorText, ColorAccent, ColorText,
	}
	for i, c := range colors {
		etherealANSI[i] = foregroundANSI(c)
	}
}

func etherealTierStyle(tier int) styleANSI {
	if tier > 0 && tier <= len(etherealANSI) {
		return etherealANSI[tier-1]
	}
	return styleANSI{}
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
	xCoords    []float64
	centres    []float64
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
	dotCols := cols * 2
	if cap(d.xCoords) < dotCols {
		d.xCoords = make([]float64, dotCols)
	} else {
		d.xCoords = d.xCoords[:dotCols]
	}
	for x := range dotCols {
		d.xCoords[x] = (float64(x) + 0.5) / float64(dotCols)
	}
	if cap(d.centres) < dotCols {
		d.centres = make([]float64, dotCols)
	} else {
		d.centres = d.centres[:dotCols]
	}
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
	d.grid.styleFor, d.grid.keepRunOnEmpty = etherealTierStyle, true
	d.grid.ensure(dotRows, dotCols)

	// Precompute the cloud centerline once per column for this frame.
	t13, t09c := t*0.13, t*0.09
	xCoords := d.xCoords
	centres := d.centres
	for x := range dotCols {
		xf := xCoords[x]
		centres[x] = 0.53 + 0.16*math.Sin(xf*6.2+t13) + 0.06*math.Sin(xf*13-t09c)
	}

	t09, t06 := t*0.09, t*0.06
	t12, t08 := t*0.12, t*0.08
	t07, t05, t11 := t*0.07, t*0.05, t*0.11
	cloudSpan := 0.18 + 0.13*bass
	rimGain := 0.42 + 0.30*bass + 0.30*mid
	cells := d.grid.cells

	for y := range dotRows {
		yf := float64(y) / float64(dotRows)
		vy := yf * 3.2
		vyWarp := vy*0.8 - t06
		vy1 := vy + t08
		vy2 := vy * 2.3
		vy3 := vy*5.1 + t11
		rowOff := y * dotCols
		for x := range dotCols {
			u := xCoords[x] * 6.8
			warp := omarchyNoiseAt(u*0.7+t09, vyWarp)
			noise := 0.60*omarchyNoiseAt(u+warp*1.8-t12, vy1) +
				0.28*omarchyNoiseAt(u*2.3+t07, vy2-warp) +
				0.12*omarchyNoiseAt(u*5.1-t05, vy3)
			distance := math.Abs(yf - centres[x] + (warp-0.5)*0.3)
			cloud := max(0, 1-distance/cloudSpan)
			halo := max(0, 1-distance/0.5)
			body := max(0, (noise-0.30)*1.4)
			rim := max(0, 1-math.Abs(noise-0.53)*13)
			light := min(1, max(0, halo*(0.06+body*0.25)+cloud*(body*0.38+rim*rimGain)))
			// A fixed threshold per dot makes wispy edges dissolve locally.
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
			cells[rowOff+x] = tier
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
