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

type etherealXCoord struct {
	x   float64
	u   float64
	u07 float64
	u23 float64
	u51 float64
}

type etherealNoiseX struct {
	i0, i1 uint8
	s      float64
}

type etherealNoiseY struct {
	r0, r1 int
	s, ns  float64
}

func etherealAxisX(coord float64) etherealNoiseX {
	const mask = omarchyNoiseSize - 1
	i := int(coord)
	if float64(i) > coord {
		i--
	}
	f := coord - float64(i)
	i0 := i & mask
	return etherealNoiseX{
		i0: uint8(i0),
		i1: uint8((i0 + 1) & mask),
		s:  f * f * (3 - 2*f),
	}
}

func etherealAxisY(coord float64) etherealNoiseY {
	const mask = omarchyNoiseSize - 1
	i := int(coord)
	if float64(i) > coord {
		i--
	}
	f := coord - float64(i)
	i0 := i & mask
	s := f * f * (3 - 2*f)
	return etherealNoiseY{
		r0: i0 * omarchyNoiseSize,
		r1: ((i0 + 1) & mask) * omarchyNoiseSize,
		s:  s,
		ns: 1 - s,
	}
}

func etherealSampleNoise(ax etherealNoiseX, ay etherealNoiseY) float64 {
	const tableMask = omarchyNoiseSize*omarchyNoiseSize - 1
	a := omarchyNoise[(ay.r0|int(ax.i0))&tableMask]
	b := omarchyNoise[(ay.r0|int(ax.i1))&tableMask]
	c := omarchyNoise[(ay.r1|int(ax.i0))&tableMask]
	d := omarchyNoise[(ay.r1|int(ax.i1))&tableMask]
	return (a+(b-a)*ax.s)*ay.ns + (c+(d-c)*ax.s)*ay.s
}

type etherealColFrame struct {
	u     float64
	warpX etherealNoiseX
	n2X   etherealNoiseX
	n3X   etherealNoiseX
}

type etherealDriver struct {
	spectrumDriverBase
	cols, rows        int
	grid              brailleGrid
	stars             []etherealStar
	xCoords           []etherealXCoord
	centres           []float64
	colFrames         []etherealColFrame
	scatterThresholds []float64
	minLights         []float64
	elapsed           float64
	lastTick          time.Time
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
	dotRows, dotCols := rows*4, cols*2
	if cap(d.xCoords) < dotCols {
		d.xCoords = make([]etherealXCoord, dotCols)
	} else {
		d.xCoords = d.xCoords[:dotCols]
	}
	for x := range dotCols {
		xf := (float64(x) + 0.5) / float64(dotCols)
		u := xf * 6.8
		d.xCoords[x] = etherealXCoord{
			x:   xf,
			u:   u,
			u07: u * 0.7,
			u23: u * 2.3,
			u51: u * 5.1,
		}
	}
	if cap(d.centres) < dotCols {
		d.centres = make([]float64, dotCols)
	} else {
		d.centres = d.centres[:dotCols]
	}
	if cap(d.colFrames) < dotCols {
		d.colFrames = make([]etherealColFrame, dotCols)
	} else {
		d.colFrames = d.colFrames[:dotCols]
	}
	need := dotRows * dotCols
	if cap(d.scatterThresholds) < need {
		d.scatterThresholds = make([]float64, need)
		d.minLights = make([]float64, need)
	} else {
		d.scatterThresholds = d.scatterThresholds[:need]
		d.minLights = d.minLights[:need]
	}
	for y := range dotRows {
		rowOff := y * dotCols
		for x := range dotCols {
			thresh := scatterHash(0, y, x, 0)
			d.scatterThresholds[rowOff+x] = thresh
			// Store a conservative lower bound on light for fast dot rejection.
			minLight := 0.08 + thresh/1.5 - 1e-9
			if minLight < 0.10-1e-9 {
				minLight = 0.10 - 1e-9
			}
			d.minLights[rowOff+x] = minLight
		}
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

	t09, t06 := t*0.09, t*0.06
	t12, t08 := t*0.12, t*0.08
	t07, t05, t11 := t*0.07, t*0.05, t*0.11
	t13 := t * 0.13

	// Precompute per-column cloud centerlines and static noise axes for this frame.
	xCoords := d.xCoords
	centres := d.centres
	colFrames := d.colFrames
	for x := range dotCols {
		xc := xCoords[x]
		centres[x] = 0.53 + 0.16*math.Sin(xc.x*6.2+t13) + 0.06*math.Sin(xc.x*13-t09)
		colFrames[x] = etherealColFrame{
			u:     xc.u,
			warpX: etherealAxisX(xc.u07 + t09),
			n2X:   etherealAxisX(xc.u23 + t07),
			n3X:   etherealAxisX(xc.u51 - t05),
		}
	}

	cloudSpan := 0.18 + 0.13*bass
	invCloudSpan := 1.0 / cloudSpan
	rimGain := 0.42 + 0.30*bass + 0.30*mid
	rimCloudCoeff := 0.12236 + rimGain
	interceptRim := 0.1405 + rimCloudCoeff
	slopeRim := 0.281 + rimCloudCoeff*invCloudSpan
	const interceptMax = 0.6774
	slopeMax := 0.61 + 0.3724*invCloudSpan
	dCross := (interceptRim - interceptMax) / (slopeRim - slopeMax)
	cells := d.grid.cells
	scatter := d.scatterThresholds
	minLights := d.minLights

	for y := range dotRows {
		yf := float64(y) / float64(dotRows)
		vy := yf * 3.2
		warpY := etherealAxisY(vy*0.8 - t06)
		n1Y := etherealAxisY(vy + t08)
		vy2 := vy * 2.3
		n3Y := etherealAxisY(vy*5.1 + t11)
		rowOff := y * dotCols
		rowCells := cells[rowOff : rowOff+dotCols]
		rowScatter := scatter[rowOff : rowOff+dotCols]
		rowMinLight := minLights[rowOff : rowOff+dotCols]
		for x := range dotCols {
			dy := yf - centres[x]
			absDy := math.Abs(dy)
			// Skip dots too far from the centerline to reach minimum light after warp.
			if absDy >= 0.487 {
				continue
			}

			cf := &colFrames[x]
			warp := etherealSampleNoise(cf.warpX, warpY)
			distance := math.Abs(dy + (warp-0.5)*0.3)
			if distance >= 0.337 {
				continue
			}
			var maxLight float64
			if distance < dCross {
				maxLight = interceptRim - distance*slopeRim
			} else if distance < cloudSpan {
				maxLight = interceptMax - distance*slopeMax
			} else {
				maxLight = 0.305 - distance*0.61
			}
			if maxLight < rowMinLight[x] {
				continue
			}

			noise := 0.60*etherealSampleNoise(etherealAxisX(cf.u+warp*1.8-t12), n1Y) +
				0.28*etherealSampleNoise(cf.n2X, etherealAxisY(vy2-warp)) +
				0.12*etherealSampleNoise(cf.n3X, n3Y)
			halo := 1 - distance*2
			body := (noise - 0.30) * 1.4
			if body < 0 {
				body = 0
			}
			light := halo * (0.06 + body*0.25)
			if distance < cloudSpan {
				cloud := 1 - distance*invCloudSpan
				if cloud > 0 {
					rim := 1 - math.Abs(noise-0.53)*13
					if rim < 0 {
						rim = 0
					}
					light += cloud * (body*0.38 + rim*rimGain)
				}
			}
			// A fixed threshold per dot makes wispy edges dissolve locally.
			if light < 0.10 || rowScatter[x] > (light-0.08)*1.5 {
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
			rowCells[x] = tier
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
