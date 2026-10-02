package ui

import (
	"math"
	"strings"
	"time"
)

const (
	classicLEDFFTSize  = 2048
	classicLEDBarWidth = 2
	classicLEDBarGap   = 1
	// Frame cadence. Real Winamp ran around 30 FPS; matching that gives the
	// characteristic chunky LED feel without burning CPU on smooth interpolation.
	// The driver owns its cadence, so the model redraws it at this rate during
	// playback, or at TickAnim with the 60 FPS setting. The physics use the
	// wall clock.
	classicLEDFPS = 30
	// Body smoothing rates. Fast attack so a kick drum lights LEDs immediately,
	// medium decay so the bar visibly settles a frame at a time.
	classicLEDRiseRate = 60.0
	classicLEDFallRate = 16.0
	// Peak cap behavior. Holds at the apex briefly, then falls at a constant
	// normalized-units-per-second rate (quantized into LED rows by render).
	classicLEDPeakHold = 0.45
	classicLEDPeakFall = 0.55
	classicLEDEpsilon  = 1e-3
)

type classicLEDDriver struct {
	body     []float64
	peak     []float64
	hold     []float64
	bandsAt  time.Time
	lastTick time.Time
}

func newClassicLEDDriver() visModeDriver {
	return &classicLEDDriver{}
}

func classicLEDBarCount(width int) int {
	return max(1, (width+classicLEDBarGap)/(classicLEDBarWidth+classicLEDBarGap))
}

func classicLEDRenderWidth(bars int) int {
	if bars <= 0 {
		return 0
	}
	return bars*(classicLEDBarWidth+classicLEDBarGap) - classicLEDBarGap
}

func (d *classicLEDDriver) AnalysisSpec(v *Visualizer) VisAnalysisSpec {
	return VisAnalysisSpec{
		BandCount: classicLEDBarCount(v.columns()),
		FFTSize:   classicLEDFFTSize,
	}
}

func (d *classicLEDDriver) OnEnter(*Visualizer) {
	*d = classicLEDDriver{}
}

func (d *classicLEDDriver) OnLeave(*Visualizer) {}

// ownsCadence lets the model redraw ClassicLED at classicLEDFPS, which is
// faster than TickFast.
func (*classicLEDDriver) ownsCadence() {}

func (d *classicLEDDriver) levels(v *Visualizer) []float64 {
	return resampleBandsLinear(v.bands, classicLEDBarCount(v.columns()))
}

func (d *classicLEDDriver) frameInterval() time.Duration {
	return time.Second / classicLEDFPS
}

func (d *classicLEDDriver) animating(v *Visualizer) bool {
	if len(d.body) == 0 {
		return false
	}
	levels := d.levels(v)
	if len(levels) != len(d.body) {
		return false
	}
	for i := range d.body {
		if d.body[i] > classicLEDEpsilon ||
			d.peak[i] > d.body[i]+classicLEDEpsilon ||
			math.Abs(d.body[i]-levels[i]) > classicLEDEpsilon {
			return true
		}
	}
	return false
}

func (d *classicLEDDriver) TickInterval(v *Visualizer, ctx VisTickContext) time.Duration {
	if ctx.OverlayActive {
		return TickSlow
	}
	if ctx.Playing || d.animating(v) {
		return d.frameInterval()
	}
	return TickSlow
}

func (d *classicLEDDriver) Tick(v *Visualizer, ctx VisTickContext) {
	if ctx.OverlayActive {
		d.bandsAt = time.Time{}
		d.lastTick = time.Time{}
		return
	}
	spec := d.AnalysisSpec(v)
	if ctx.Playing {
		if d.bandsAt.IsZero() || ctx.Now.Sub(d.bandsAt) >= TickAnalyze {
			if ctx.Analyze != nil {
				v.bands = ctx.Analyze(spec)
			}
			d.bandsAt = ctx.Now
		}
	} else {
		d.bandsAt = time.Time{}
		v.bands = v.Analyze(nil, spec)
	}
	d.advance(v, ctx.Now)
}

func (d *classicLEDDriver) advance(v *Visualizer, now time.Time) {
	levels := d.levels(v)
	if len(d.body) != len(levels) || len(d.peak) != len(levels) || len(d.hold) != len(levels) {
		d.body = append(d.body[:0], levels...)
		d.peak = append(d.peak[:0], levels...)
		if cap(d.hold) >= len(levels) {
			d.hold = d.hold[:len(levels)]
			clear(d.hold)
		} else {
			d.hold = make([]float64, len(levels))
		}
		d.lastTick = now
		return
	}

	dtS := clampFrameDT(now, d.lastTick, d.frameInterval()).Seconds()
	d.lastTick = now

	for i, target := range levels {
		d.body[i] = easeToward(d.body[i], target, classicLEDRiseRate, classicLEDFallRate, dtS)

		switch {
		case d.body[i] >= d.peak[i]:
			d.peak[i] = d.body[i]
			d.hold[i] = classicLEDPeakHold
		case d.hold[i] > 0:
			d.hold[i] = max(0, d.hold[i]-dtS)
		default:
			d.peak[i] = max(d.body[i], d.peak[i]-classicLEDPeakFall*dtS)
		}
	}
}

func (d *classicLEDDriver) Render(v *Visualizer) string {
	height := v.Rows
	width := v.columns()
	bars := classicLEDBarCount(width)
	body, peak := d.renderState(v, bars)

	rowPad := max(0, width-classicLEDRenderWidth(bars))
	heightF := float64(height)

	lines := make([]string, height)
	gap := strings.Repeat(" ", classicLEDBarGap)
	for row := range height {
		rowBottom := float64(height-1-row) / heightF
		// Row position from the bottom: 0 == lowest LED row, height-1 == top.
		rfb := height - 1 - row

		var content strings.Builder
		if rowPad > 0 {
			content.WriteString(strings.Repeat(" ", rowPad))
		}
		for b := range bars {
			lit := int(math.Floor(body[b]*heightF + 1e-6))
			// Quantize peak to a row index. A peak only renders when it sits
			// strictly above the bar body; otherwise it merges with the LED.
			peakSeg := int(math.Floor(peak[b]*heightF + 1e-6))
			if peakSeg >= height {
				peakSeg = height - 1
			}
			showPeak := peak[b] > body[b]+0.5/heightF && peakSeg >= lit

			var glyph string
			switch {
			case rfb < lit:
				glyph = "▄"
			case showPeak && rfb == peakSeg:
				glyph = "▀"
			default:
				glyph = " "
			}
			for range classicLEDBarWidth {
				content.WriteString(glyph)
			}
			if b < bars-1 {
				content.WriteString(gap)
			}
		}
		lines[row] = specWrap(rowBottom, content.String())
	}
	return strings.Join(lines, "\n")
}

func (d *classicLEDDriver) renderState(v *Visualizer, bars int) (body, peak []float64) {
	if len(d.body) == bars && len(d.peak) == bars {
		return d.body, d.peak
	}
	levels := d.levels(v)
	if len(levels) != bars {
		levels = make([]float64, bars)
	}
	return levels, levels
}
