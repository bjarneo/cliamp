package ui

import (
	"math"
	"strings"
	"time"
)

const (
	stereoSampleWindow = 2048
	stereoFloorDB      = -48.0
	stereoRiseRate     = 36.0
	stereoFallRate     = 10.0
	stereoPeakHold     = 450 * time.Millisecond
	stereoPeakFallRate = 0.65
	stereoEpsilon      = 1e-3
)

type stereoDriver struct {
	samples     [][2]float64
	level       [2]float64
	peak        [2]float64
	hold        [2]time.Duration
	targetLevel [2]float64
	targetPeak  [2]float64
	lastTick    time.Time
	samplesAt   time.Time
}

func newStereoDriver() visModeDriver {
	return &stereoDriver{samples: make([][2]float64, stereoSampleWindow)}
}

func (*stereoDriver) AnalysisSpec(*Visualizer) VisAnalysisSpec {
	return spectrumAnalysisSpec(0)
}

func (d *stereoDriver) Render(v *Visualizer) string {
	height := v.Rows
	if height <= 0 {
		return ""
	}
	lines := make([]string, height)

	if height == 1 {
		lines[0] = renderStereoMeter("L ", d.level[0], d.peak[0], PanelWidth)
		return strings.Join(lines, "\n")
	}

	thickness := height / 2
	row := 0

	for i := range thickness {
		label := "  "
		if i == thickness/2 {
			label = "L "
		}
		lines[row+i] = renderStereoMeter(label, d.level[0], d.peak[0], PanelWidth)
	}
	row += thickness
	if height%2 != 0 {
		row++
	}
	for i := range thickness {
		label := "  "
		if i == thickness/2 {
			label = "R "
		}
		lines[row+i] = renderStereoMeter(label, d.level[1], d.peak[1], PanelWidth)
	}

	return strings.Join(lines, "\n")
}

func (d *stereoDriver) Tick(_ *Visualizer, ctx VisTickContext) {
	if ctx.OverlayActive {
		d.lastTick = time.Time{}
		d.samplesAt = time.Time{}
		return
	}

	if ctx.Playing {
		d.sample(ctx)
	} else {
		d.targetLevel = [2]float64{}
		d.targetPeak = [2]float64{}
		d.samplesAt = time.Time{}
	}
	d.advance(ctx.Now)
	if !d.animating() {
		// At rest the meters still hold a sub-epsilon residual and the
		// renderer marks any peak above zero. Zero them so the panel goes dark.
		d.level = [2]float64{}
		d.peak = [2]float64{}
	}
}

func (d *stereoDriver) TickInterval(_ *Visualizer, ctx VisTickContext) time.Duration {
	if ctx.OverlayActive {
		return TickSlow
	}
	if ctx.Playing || d.animating() {
		return TickAnim
	}
	return TickSlow
}

func (d *stereoDriver) OnEnter(*Visualizer) {
	samples := d.samples
	*d = stereoDriver{samples: samples}
}

func (*stereoDriver) OnLeave(*Visualizer) {}

func (d *stereoDriver) sample(ctx VisTickContext) {
	if ctx.StereoSamplesInto == nil {
		d.targetLevel = [2]float64{}
		d.targetPeak = [2]float64{}
		return
	}
	if !d.samplesAt.IsZero() && !ctx.Now.IsZero() && ctx.Now.Sub(d.samplesAt) < TickAnalyze {
		return
	}
	n := ctx.StereoSamplesInto(d.samples)
	d.targetLevel, d.targetPeak = stereoMetrics(d.samples[:n])
	if !ctx.Now.IsZero() {
		d.samplesAt = ctx.Now
	}
}

func (d *stereoDriver) advance(now time.Time) {
	dt := TickAnim
	if !now.IsZero() && !d.lastTick.IsZero() {
		dt = now.Sub(d.lastTick)
	}
	if dt <= 0 || dt > maxSmoothDtFrames*TickAnim {
		dt = TickAnim
	}
	d.lastTick = now
	dtSeconds := dt.Seconds()

	for channel := range 2 {
		rate := stereoFallRate
		if d.targetLevel[channel] > d.level[channel] {
			rate = stereoRiseRate
		}
		d.level[channel] += (d.targetLevel[channel] - d.level[channel]) * (1 - math.Exp(-rate*dtSeconds))

		switch {
		case d.targetPeak[channel] > d.peak[channel]:
			d.peak[channel] = d.targetPeak[channel]
			d.hold[channel] = stereoPeakHold
		case d.hold[channel] > 0:
			d.hold[channel] = max(0, d.hold[channel]-dt)
		default:
			d.peak[channel] = max(d.level[channel], d.peak[channel]-stereoPeakFallRate*dtSeconds)
		}
	}

}

func (d *stereoDriver) animating() bool {
	for channel := range 2 {
		if d.level[channel] > stereoEpsilon || d.peak[channel] > stereoEpsilon ||
			math.Abs(d.level[channel]-d.targetLevel[channel]) > stereoEpsilon {
			return true
		}
	}
	return false
}

func stereoMetrics(samples [][2]float64) (level, peak [2]float64) {
	if len(samples) == 0 {
		return level, peak
	}

	var sumSquares [2]float64
	for _, sample := range samples {
		for channel := range 2 {
			value := sample[channel]
			sumSquares[channel] += value * value
			peak[channel] = max(peak[channel], math.Abs(value))
		}
	}

	for channel := range 2 {
		rms := math.Sqrt(sumSquares[channel] / float64(len(samples)))
		level[channel] = stereoDBLevel(rms)
		peak[channel] = stereoDBLevel(peak[channel])
	}
	return level, peak
}

func stereoDBLevel(amplitude float64) float64 {
	if amplitude <= 0 {
		return 0
	}
	db := 20 * math.Log10(amplitude)
	return max(0, min(1, (db-stereoFloorDB)/-stereoFloorDB))
}

func renderStereoMeter(label string, level, peak float64, width int) string {
	if width <= 0 {
		return ""
	}
	if width <= len(label) {
		return label[:width]
	}

	cells := width - len(label)
	lit := min(cells, int(math.Round(level*float64(cells))))
	peakCell := -1
	if peak > 0 {
		peakCell = min(cells-1, max(0, int(math.Round(peak*float64(cells)))-1))
	}

	var line strings.Builder
	line.Grow(width * 2)
	line.WriteString(label)
	var run strings.Builder
	runTag := -1
	for cell := range cells {
		glyph := "·"
		tag := -1
		if cell < lit {
			glyph = "▮"
			tag = specTag(float64(cell) / float64(max(1, cells-1)))
		}
		if cell == peakCell {
			glyph = "■"
			tag = specTag(float64(cell) / float64(max(1, cells-1)))
		}
		if tag != runTag {
			flushStyleRun(&line, &run, runTag)
			runTag = tag
		}
		run.WriteString(glyph)
	}
	flushStyleRun(&line, &run, runTag)
	return line.String()
}
