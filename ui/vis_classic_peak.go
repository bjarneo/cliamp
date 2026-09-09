package ui

import (
	"math"
	"strings"
	"time"
)

const (
	classicPeakSpectrumBands = 64
	classicPeakFFTSize       = 4096
	// Default animation timestep when elapsed time is missing or non-positive (~60 Hz).
	tickClassicPeak = time.Second / 60
	// Minimum frame rate used when deriving redraw interval from terminal size.
	classicPeakMinFPS = 24.0
	// Maximum frame rate used when deriving redraw interval from terminal size.
	classicPeakMaxFPS = 60.0
	// Divides the FFT window duration to set spectrum analysis hop size (overlap factor).
	classicPeakFFTOverlap = 2.0
	// Minimum spacing between spectrum analyses, regardless of sample rate.
	classicPeakSampleFloor = 20 * time.Millisecond
	// Minimum upward launch velocity for a newly detached peak cap.
	classicPeakLaunchBase = 0.8
	// Extra launch velocity added in proportion to the bar's rise amount.
	classicPeakLaunchGain = 1.4
	// Maximum upward launch velocity for the peak cap.
	classicPeakLaunchMax = 1.7
	// Downward acceleration applied to the peak cap after launch.
	classicPeakGravity = 9.5
	// Time the peak cap pauses at the apex before falling.
	classicPeakApexHold = 0.08
	// Rendered width of each spectrum bar in terminal cells.
	classicPeakBarWidth = 1
	// Number of spaces inserted between adjacent bars.
	classicPeakBarGap = 1
	// Smoothing rate used when bar bodies move upward.
	classicPeakBarRiseRate = 34.0
	// Smoothing rate used when bar bodies move downward.
	classicPeakBarFallRate = 10.0
	// Highest normalized height a peak cap may reach.
	classicPeakMaxHeight = 1.0
	// Small tolerance for treating peak and bar positions as visually equal.
	classicPeakVisibleEpsilon = 0.01
)

var classicPeakGlyphs = [4]rune{
	'⎺',
	'⎻',
	'⎼',
	'⎽',
}

type classicPeakDriver struct {
	barPos     []float64
	peakPos    []float64
	peakVel    []float64
	peakHold   []float64
	lastTick   time.Time
	bandsAt    time.Time // nominal hop boundary of the last analysis
	analyzedAt time.Time // wall clock of the last analysis
}

func newClassicPeakDriver() visModeDriver {
	return &classicPeakDriver{}
}

func (*classicPeakDriver) AnalysisSpec(*Visualizer) VisAnalysisSpec {
	return VisAnalysisSpec{
		BandCount: classicPeakSpectrumBands,
		FFTSize:   classicPeakFFTSize,
	}
}

func (d *classicPeakDriver) Render(v *Visualizer) string {
	height := v.Rows
	cols, peaks := d.renderState(v)
	rowPad := max(0, PanelWidth-classicPeakRenderWidth(len(cols)))

	lines := make([]string, height)
	for row := range height {
		var content strings.Builder
		if rowPad > 0 {
			content.WriteString(strings.Repeat(" ", rowPad))
		}
		rowBottom := float64(height-1-row) / float64(height)
		rowTop := float64(height-row) / float64(height)

		for col, level := range cols {
			capVisible := classicPeakDetached(level, peaks[col], height)
			capRow, capGlyph := classicPeakGlyph(peaks[col], height)
			cell := fracBlock(level, rowBottom, rowTop)

			if capVisible && row == capRow {
				cell = string(capGlyph)
			}
			content.WriteString(strings.Repeat(cell, classicPeakBarWidth))
			if col < len(cols)-1 {
				content.WriteString(strings.Repeat(" ", classicPeakBarGap))
			}
		}

		lines[row] = specWrap(rowBottom, content.String())
	}

	return strings.Join(lines, "\n")
}

func (d *classicPeakDriver) Tick(v *Visualizer, ctx VisTickContext) {
	if ctx.OverlayActive {
		d.bandsAt = time.Time{}
		d.lastTick = time.Time{}
		return
	}
	if ctx.Playing {
		interval := d.analysisInterval(v)
		hopDue := d.bandsAt.IsZero() || ctx.Now.Sub(d.bandsAt) >= interval
		floorDue := d.analyzedAt.IsZero() || ctx.Now.Sub(d.analyzedAt) >= classicPeakSampleFloor
		if hopDue && floorDue {
			if ctx.Analyze != nil {
				v.bands = ctx.Analyze(d.AnalysisSpec(v))
			}
			d.analyzedAt = ctx.Now
			// Keep the nominal hop phase so redraw quantization does not lower
			// the average analysis rate. The floor above is measured from the
			// real analysis time so it survives interval changes.
			if d.bandsAt.IsZero() {
				d.bandsAt = ctx.Now
			} else {
				elapsed := ctx.Now.Sub(d.bandsAt)
				d.bandsAt = d.bandsAt.Add(elapsed - elapsed%interval)
			}
		}
	} else {
		d.bandsAt = time.Time{}
		v.bands = v.Analyze(nil, d.AnalysisSpec(v))
	}
	d.sync(v)
	if d.animating(v) {
		d.advance(v, ctx.Now)
	} else {
		// Fresh motion starts with one nominal frame after resting.
		d.lastTick = time.Time{}
	}
}

func (d *classicPeakDriver) TickInterval(v *Visualizer, ctx VisTickContext) time.Duration {
	if ctx.OverlayActive {
		return TickSlow
	}
	if ctx.Playing || d.animating(v) {
		return d.frameInterval(v)
	}
	return TickSlow
}

func (d *classicPeakDriver) OnEnter(*Visualizer) {
	*d = classicPeakDriver{}
}

func (d *classicPeakDriver) OnLeave(*Visualizer) {}

func (d *classicPeakDriver) animating(v *Visualizer) bool {
	levels := d.levels(v)
	if len(levels) != len(d.barPos) || len(levels) != len(d.peakPos) {
		return false
	}
	for i, vel := range d.peakVel {
		if math.Abs(d.barPos[i]-levels[i]) > classicPeakVisibleEpsilon ||
			vel != 0 || d.peakPos[i] > d.barPos[i]+classicPeakVisibleEpsilon {
			return true
		}
	}
	return false
}

func (d *classicPeakDriver) levels(v *Visualizer) []float64 {
	activeCols := classicPeakColsForWidth(PanelWidth)
	return resampleBandsLinear(v.bands, activeCols)
}

func (d *classicPeakDriver) frameInterval(v *Visualizer) time.Duration {
	rows := DefaultVisRows
	if v != nil && v.Rows > rows {
		rows = v.Rows
	}
	fps := classicPeakLaunchMax * float64(rows*len(classicPeakGlyphs))
	fps = min(classicPeakMaxFPS, max(classicPeakMinFPS, fps))
	return time.Duration(float64(time.Second) / fps)
}

func (d *classicPeakDriver) analysisInterval(v *Visualizer) time.Duration {
	interval := d.frameInterval(v)
	if v == nil || v.sr <= 0 {
		return interval
	}
	spec := d.AnalysisSpec(v)
	window := time.Duration(float64(time.Second) * float64(spec.FFTSize) / v.sr)
	if window <= 0 {
		return interval
	}
	sampleInterval := max(classicPeakSampleFloor, time.Duration(float64(window)/classicPeakFFTOverlap))
	return max(interval, sampleInterval)
}

func classicPeakGlyph(level float64, height int) (row int, glyph rune) {
	dotRows := max(1, height*4)
	dotY := int(math.Round((1 - min(1.0, level)) * float64(dotRows-1)))
	row = dotY / 4
	glyph = classicPeakGlyphs[dotY%4]
	return row, glyph
}

func classicPeakDetached(level, peak float64, height int) bool {
	return peak > level+classicPeakVisibleGap(height)
}

func classicPeakVisibleGap(height int) float64 {
	// Half a cap subcell filters motion that cannot be rendered reliably.
	return max(classicPeakVisibleEpsilon, 0.5/float64(max(1, height*4)))
}

func classicPeakColsForWidth(width int) int {
	return max(1, (width+classicPeakBarGap)/(classicPeakBarWidth+classicPeakBarGap))
}

func classicPeakRenderWidth(cols int) int {
	if cols <= 0 {
		return 0
	}
	return (classicPeakBarWidth+classicPeakBarGap)*cols - classicPeakBarGap
}

func classicPeakStep(current, target, dt float64) float64 {
	rate := classicPeakBarFallRate
	if target > current {
		rate = classicPeakBarRiseRate
	}
	return current + (target-current)*(1-math.Exp(-rate*dt))
}

func (d *classicPeakDriver) landed(i int) bool {
	return d.peakVel[i] == 0 && d.peakPos[i] <= d.barPos[i]+classicPeakVisibleEpsilon
}

func (d *classicPeakDriver) reset(levels []float64, now time.Time) {
	d.barPos = make([]float64, len(levels))
	copy(d.barPos, levels)
	d.peakPos = make([]float64, len(levels))
	copy(d.peakPos, levels)
	if cap(d.peakVel) >= len(levels) {
		d.peakVel = d.peakVel[:len(levels)]
		clear(d.peakVel)
	} else {
		d.peakVel = make([]float64, len(levels))
	}
	if cap(d.peakHold) >= len(levels) {
		d.peakHold = d.peakHold[:len(levels)]
		clear(d.peakHold)
	} else {
		d.peakHold = make([]float64, len(levels))
	}
	d.lastTick = now
}

func (d *classicPeakDriver) sync(v *Visualizer) {
	levels := d.levels(v)
	if len(levels) != len(d.barPos) || len(levels) != len(d.peakPos) {
		d.reset(levels, time.Time{})
		return
	}
	gap := classicPeakVisibleGap(v.Rows)
	for i, level := range levels {
		if d.landed(i) && level > d.peakPos[i]+gap {
			delta := level - d.peakPos[i]
			d.peakPos[i] = level
			d.peakVel[i] = min(classicPeakLaunchMax, classicPeakLaunchBase+classicPeakLaunchGain*delta)
			d.peakHold[i] = 0
		}
	}
}

func (d *classicPeakDriver) advance(v *Visualizer, now time.Time) {
	levels := d.levels(v)
	if len(levels) != len(d.barPos) || len(levels) != len(d.peakPos) {
		d.reset(levels, now)
		return
	}

	dtSeconds := tickClassicPeak.Seconds()
	if !now.IsZero() && !d.lastTick.IsZero() {
		dtSeconds = now.Sub(d.lastTick).Seconds()
	}
	// Long gaps are suspension, not animation time. Resume with one frame.
	if dtSeconds <= 0 || dtSeconds > 10*tickClassicPeak.Seconds() {
		dtSeconds = tickClassicPeak.Seconds()
	}
	d.lastTick = now

	for i, level := range levels {
		d.barPos[i] = classicPeakStep(d.barPos[i], level, dtSeconds)

		remaining := dtSeconds
		if d.peakVel[i] > 0 {
			// Apex within this frame: land there, start the hold, and spend
			// the rest of the frame on the hold and the fall.
			apexAt := d.peakVel[i] / classicPeakGravity
			if apexAt <= remaining {
				d.peakPos[i] = min(classicPeakMaxHeight, d.peakPos[i]+0.5*d.peakVel[i]*apexAt)
				d.peakVel[i] = 0
				remaining -= apexAt
				d.peakHold[i] = classicPeakApexHold
			}
		}
		if d.peakHold[i] > 0 {
			if remaining <= d.peakHold[i] {
				d.peakHold[i] -= remaining
				continue
			}
			remaining -= d.peakHold[i]
			d.peakHold[i] = 0
		}

		d.peakPos[i] = min(classicPeakMaxHeight, d.peakPos[i]+d.peakVel[i]*remaining-0.5*classicPeakGravity*remaining*remaining)
		d.peakVel[i] -= classicPeakGravity * remaining
		if d.peakPos[i] <= d.barPos[i]+classicPeakVisibleEpsilon {
			d.peakPos[i] = d.barPos[i]
			d.peakVel[i] = 0
			d.peakHold[i] = 0
		}
	}
}

func (d *classicPeakDriver) renderState(v *Visualizer) ([]float64, []float64) {
	levels := d.levels(v)
	if len(levels) != len(d.barPos) || len(levels) != len(d.peakPos) {
		return levels, levels
	}
	return d.barPos, d.peakPos
}
