package ui

import "time"

type VisTickContext struct {
	Now               time.Time
	Playing           bool
	Paused            bool
	OverlayActive     bool
	Analyze           func(VisAnalysisSpec) []float64
	StereoSamplesInto func([][2]float64) int
}

// VisTap picks the audio tap that feeds an analysis.
type VisTap int

const (
	// VisTapLatest reads the last frames that the decoder handed to the
	// audio output.
	VisTapLatest VisTap = iota
	// VisTapAudible reads the frames that play now, interpolated from the
	// wall clock, so the window moves on every tick.
	VisTapAudible
)

type VisAnalysisSpec struct {
	BandCount int
	FFTSize   int
	Tap       VisTap
}

func spectrumAnalysisSpec(bandCount int) VisAnalysisSpec {
	return VisAnalysisSpec{
		BandCount: bandCount,
		FFTSize:   defaultFFTSize,
	}
}

// NormalizeAnalysisSpec fills the defaults of spec. A raw-sample spec, with no
// bands, always reads the audible tap.
func NormalizeAnalysisSpec(spec VisAnalysisSpec) VisAnalysisSpec {
	if spec.BandCount < 0 {
		spec.BandCount = 0
	}
	if spec.FFTSize <= 0 {
		spec.FFTSize = defaultFFTSize
	}
	if spec.BandCount == 0 {
		spec.Tap = VisTapAudible
	}
	return spec
}

type visModeDriver interface {
	AnalysisSpec(*Visualizer) VisAnalysisSpec
	Render(*Visualizer) string
	Tick(*Visualizer, VisTickContext)
	TickInterval(*Visualizer, VisTickContext) time.Duration
	OnEnter(*Visualizer)
	OnLeave(*Visualizer)
}

type visPauseSettler interface {
	pauseSettled() bool
}

// visCadenceOwner marks a driver whose TickInterval the model follows during
// playback, even when it is faster than TickFast. The model ticks the
// raw-sample modes at TickAnim during playback. It ticks every other driver
// no faster than TickFast, unless the 60 FPS setting is on.
type visCadenceOwner interface {
	ownsCadence()
}

type renderOnlyDriver struct {
	spec         VisAnalysisSpec
	render       func(*Visualizer, []float64) string
	tickDuration time.Duration // 0 = use defaultDriverTickInterval
}

func (d *renderOnlyDriver) AnalysisSpec(*Visualizer) VisAnalysisSpec {
	return d.spec
}

func (d *renderOnlyDriver) Render(v *Visualizer) string {
	return d.render(v, v.SmoothedBands())
}

func (d *renderOnlyDriver) Tick(v *Visualizer, ctx VisTickContext) {
	defaultDriverTick(v, ctx, d.spec)
}

func (d *renderOnlyDriver) TickInterval(_ *Visualizer, ctx VisTickContext) time.Duration {
	if d.tickDuration > 0 && ctx.Playing && !ctx.OverlayActive {
		return d.tickDuration
	}
	return defaultDriverTickInterval(ctx)
}

func (*renderOnlyDriver) OnEnter(*Visualizer) {}

func (*renderOnlyDriver) OnLeave(*Visualizer) {}

// spectrumDriverBase gives a stateful spectrum driver the default analysis
// spec, the default tick cadence and an empty OnLeave. A driver embeds it and
// writes only its own Tick, Render and OnEnter.
type spectrumDriverBase struct{}

func (spectrumDriverBase) AnalysisSpec(*Visualizer) VisAnalysisSpec {
	return spectrumAnalysisSpec(DefaultSpectrumBands)
}

func (spectrumDriverBase) TickInterval(_ *Visualizer, ctx VisTickContext) time.Duration {
	return defaultDriverTickInterval(ctx)
}

func (spectrumDriverBase) OnLeave(*Visualizer) {}

type noOpDriver struct{}

func (*noOpDriver) AnalysisSpec(*Visualizer) VisAnalysisSpec { return VisAnalysisSpec{} }

func (*noOpDriver) Render(*Visualizer) string { return "" }

func (*noOpDriver) Tick(*Visualizer, VisTickContext) {}

func (*noOpDriver) TickInterval(*Visualizer, VisTickContext) time.Duration { return TickSlow }

func (*noOpDriver) OnEnter(*Visualizer) {}

func (*noOpDriver) OnLeave(*Visualizer) {}

func newRenderOnlyDriver(spec VisAnalysisSpec, render func(*Visualizer, []float64) string) func() visModeDriver {
	return func() visModeDriver {
		return &renderOnlyDriver{spec: NormalizeAnalysisSpec(spec), render: render}
	}
}

func newFastRenderOnlyDriver(spec VisAnalysisSpec, tick time.Duration, render func(*Visualizer, []float64) string) func() visModeDriver {
	return func() visModeDriver {
		return &renderOnlyDriver{spec: NormalizeAnalysisSpec(spec), render: render, tickDuration: tick}
	}
}

func newNoOpDriver() visModeDriver {
	return &noOpDriver{}
}

func defaultDriverTick(v *Visualizer, ctx VisTickContext, spec VisAnalysisSpec) {
	if ctx.OverlayActive {
		// Reset both clocks so the first tick after dismissal analyzes
		// immediately and smoothing dt resets to a single-frame step.
		v.lastAnalyzeAt = time.Time{}
		v.lastSmoothTick = time.Time{}
		return
	}
	spec = NormalizeAnalysisSpec(spec)
	if ctx.Analyze != nil {
		// Decouple FFT cadence from animation cadence. Raw-sample modes have no
		// FFT work, so refresh their waveform on every render tick.
		due := spec.BandCount == 0 || v.lastAnalyzeAt.IsZero() || ctx.Now.IsZero() ||
			ctx.Now.Sub(v.lastAnalyzeAt) >= TickAnalyze
		if due {
			bands := ctx.Analyze(spec)
			if spec.BandCount > 0 {
				v.bands = bands
			}
			if !ctx.Now.IsZero() {
				v.lastAnalyzeAt = ctx.Now
			}
		}
	}
	// Always ease toward the most recent target — even when Analyze is nil
	// or skipped — so animation stays smooth across analysis gaps.
	if spec.BandCount > 0 {
		v.advanceSmoothing(ctx.Now)
	}
}

// defaultDriverTickInterval uses fast ticks only when audio is actively playing
// with a live visualizer. Paused/stopped playback has no new audio samples, so
// slow ticks are sufficient and save CPU/GPU repaints. Overlays use slow ticks
// as well. Bar-style spectrum drivers opt into TickAnim via newFastRenderOnlyDriver.
func defaultDriverTickInterval(ctx VisTickContext) time.Duration {
	if ctx.OverlayActive {
		return TickSlow
	}
	if ctx.Playing {
		return TickFast
	}
	return TickSlow
}
