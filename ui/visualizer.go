package ui

import "time"

const (
	DefaultSpectrumBands = 10
	defaultFFTSize       = 2048
	DefaultVisRows       = 7
	minSpectrumHz        = 20.0
	maxSpectrumHz        = 20000.0
	// Cap on dt fed into easing and peak physics, in frames. See clampFrameDT.
	maxSmoothDtFrames        = 10
	maxAnimationCatchUpSteps = 4
	// Band level below which paused spectrum content is treated as fully
	// decayed to rest, letting the model drop the visualizer to the idle tick.
	pausedDecayEpsilon = 0.01
)

// Visualizer performs FFT analysis and renders spectrum bars.
type Visualizer struct {
	prevBySpec      map[VisAnalysisSpec][]float64
	edgeCache       map[int][]float64
	fftBufCache     map[int][]float64
	fftCplxCache    map[int][]complex128 // reusable in-place FFT work buffers
	fftTwiddleCache map[int][]complex128 // precomputed roots of unity per FFT size
	windowCache     map[int][]float64
	resultBufCache  map[VisAnalysisSpec][]float64 // reusable output buffers for Analyze(), keyed by spec
	bands           []float64
	smoothedBands   []float64 // bands with sub-tick exponential easing toward v.bands
	lastSmoothTick  time.Time // wall clock of the last advanceSmoothing call
	lastAnalyzeAt   time.Time // wall clock of the last FFT analysis
	sr              float64
	Mode            VisMode
	Cols            int       // display width in terminal cells
	Rows            int       // display height in terminal rows (default DefaultVisRows)
	waveBuf         []float64 // raw samples for wave mode
	traceYBuf       []int     // per-frame y positions, see traceYs
	frame           uint64    // elapsed-time animation clock
	lastFrameTick   time.Time // wall clock of the previous frame-accounting tick
	frameElapsed    time.Duration
	frameInterval   time.Duration
	sampleBuf       []float64 // reusable buffer for reading audio tap samples
	drivers         [VisCount]visModeDriver
	activeMode      VisMode
	activeModeSet   bool
	refreshPending  bool
	luaVisNames     []string
	luaHost         LuaVisHost
	luaDriverCache  map[int]visModeDriver
	pulseCoordCache *pulseCoords
	dotMask         []bool      // per-frame Braille dots, see dotMaskFor
	dotGrid         brailleGrid // per-frame tier grid of the render-only Braille modes
}

// NewVisualizer creates a Visualizer for the given sample rate.
func NewVisualizer(sampleRate float64) *Visualizer {
	return &Visualizer{
		sr:              sampleRate,
		sampleBuf:       make([]float64, defaultFFTSize),
		Rows:            DefaultVisRows,
		bands:           make([]float64, DefaultSpectrumBands),
		prevBySpec:      make(map[VisAnalysisSpec][]float64),
		edgeCache:       make(map[int][]float64),
		fftBufCache:     make(map[int][]float64),
		fftCplxCache:    make(map[int][]complex128),
		fftTwiddleCache: make(map[int][]complex128),
		windowCache:     make(map[int][]float64),
		resultBufCache:  make(map[VisAnalysisSpec][]float64),
		luaDriverCache:  make(map[int]visModeDriver),
		refreshPending:  true,
	}
}

// Render dispatches to the active visualizer mode.
func (v *Visualizer) Render() string {
	if v == nil || v.Mode == VisNone || v.Rows <= 0 {
		return ""
	}
	cols := v.columns()
	if cols <= 0 {
		return ""
	}

	driver := v.syncDriverMode()
	if driver == nil {
		return ""
	}
	return fitVisualizerFrame(driver.Render(v), cols, v.Rows)
}

func (v *Visualizer) RequestRefresh() {
	if v != nil {
		v.refreshPending = true
	}
}

func (v *Visualizer) ConsumeRefresh() bool {
	if v == nil || !v.refreshPending {
		return false
	}
	v.refreshPending = false
	return true
}

// SmoothedBands returns the eased per-frame band values used by spectrum
// renderers. Falls back to the raw bands until smoothing has run at least
// once.
func (v *Visualizer) SmoothedBands() []float64 {
	if v == nil {
		return nil
	}
	if len(v.smoothedBands) == len(v.bands) && len(v.smoothedBands) > 0 {
		return v.smoothedBands
	}
	return v.bands
}

// advanceSmoothing eases v.smoothedBands toward v.bands using the same
// fast-attack / slow-decay shape as classicPeak's per-bar smoothing
// (classicPeakStep), so every spectrum visualizer glides between FFT samples
// instead of snapping at the analysis rate.
func (v *Visualizer) advanceSmoothing(now time.Time) {
	if v == nil || len(v.bands) == 0 {
		return
	}
	if len(v.smoothedBands) != len(v.bands) {
		// First frame after a spec change snaps to the current analysis output
		// so existing levels appear immediately instead of fading in from zero.
		v.smoothedBands = append(v.smoothedBands[:0], v.bands...)
		v.lastSmoothTick = now
		return
	}
	dt := clampFrameDT(now, v.lastSmoothTick, TickAnim).Seconds()
	v.lastSmoothTick = now
	for i, target := range v.bands {
		v.smoothedBands[i] = classicPeakStep(v.smoothedBands[i], target, dt)
	}
}

// Frame returns the current animation frame counter.
func (v *Visualizer) Frame() uint64 { return v.frame }

// RefreshPending reports whether a refresh has been requested.
func (v *Visualizer) RefreshPending() bool { return v != nil && v.refreshPending }

func (v *Visualizer) TickInterval(ctx VisTickContext) time.Duration {
	driver := v.syncDriverMode()
	if driver == nil {
		return TickSlow
	}
	if ctx.Paused {
		return TickSlow
	}
	return driver.TickInterval(v, ctx)
}

// DriverOwnsCadence reports whether the model should tick the active mode at
// its own TickInterval during playback. See visCadenceOwner.
func (v *Visualizer) DriverOwnsCadence() bool {
	_, ok := v.syncDriverMode().(visCadenceOwner)
	return ok
}

// UsesRawSamples reports whether the active visualizer draws directly from audio samples.
func (v *Visualizer) UsesRawSamples() bool {
	driver := v.syncDriverMode()
	return driver != nil && NormalizeAnalysisSpec(driver.AnalysisSpec(v)).BandCount == 0
}

func (v *Visualizer) Tick(ctx VisTickContext) {
	if v == nil || v.Rows <= 0 {
		return
	}
	if v.columns() <= 0 {
		return
	}

	driver := v.syncDriverMode()
	if driver == nil {
		return
	}
	v.refreshPending = false
	if ctx.Paused {
		if spec := NormalizeAnalysisSpec(driver.AnalysisSpec(v)); spec.BandCount == 0 {
			v.waveBuf = v.waveBuf[:0]
		}
		// Keep easing the visual down to rest instead of freezing mid-frame.
		// Drivers already know how to decay when not playing (silent band
		// analysis, target-zero physics); we just keep ticking until settled.
		if v.pausedSettled(driver, ctx) {
			v.Suspend()
		} else {
			driver.Tick(v, ctx)
		}
		return
	}
	if ctx.OverlayActive {
		v.resetFrameTiming()
	} else if v.Mode != VisNone {
		v.frame += v.animationSteps(ctx.Now, driver.TickInterval(v, ctx))
	}
	driver.Tick(v, ctx)
}

// Suspend resets elapsed-time accounting and the active driver's wall clock so
// resuming after a hidden or paused interval advances by one frame, not the gap.
func (v *Visualizer) Suspend() {
	if v == nil {
		return
	}
	v.resetFrameTiming()
	driver := v.syncDriverMode()
	if driver != nil {
		driver.Tick(v, VisTickContext{OverlayActive: true})
	}
}

func (v *Visualizer) resetFrameTiming() {
	v.lastFrameTick = time.Time{}
	v.frameElapsed = 0
	v.frameInterval = 0
}

// pausedSettled reports whether a paused visualizer has no content left to
// ease down, so it can freeze at rest. Band-driven modes must empty both the
// raw and smoothed bands, raw-sample modes must clear their waveform, and
// stateful drivers must finish their own animation. Classic meters signal
// animation through their tick interval; particle modes implement
// visPauseSettler.
func (v *Visualizer) pausedSettled(driver visModeDriver, ctx VisTickContext) bool {
	if v == nil || driver == nil {
		return true
	}
	spec := NormalizeAnalysisSpec(driver.AnalysisSpec(v))
	if spec.BandCount == 0 && len(v.waveBuf) > 0 {
		return false
	}
	if spec.BandCount > 0 {
		for _, b := range v.bands {
			if b >= pausedDecayEpsilon {
				return false
			}
		}
		for _, b := range v.smoothedBands {
			if b >= pausedDecayEpsilon {
				return false
			}
		}
	}
	if settler, ok := driver.(visPauseSettler); ok && !settler.pauseSettled() {
		return false
	}
	ctx.Playing = false
	return driver.TickInterval(v, ctx) >= TickSlow
}

// PausedDecayPending reports whether a paused visualizer still needs ticks to
// settle its content to rest. The model uses it to keep an active tick cadence
// instead of dropping to fully idle while content eases down.
func (v *Visualizer) PausedDecayPending(ctx VisTickContext) bool {
	driver := v.syncDriverMode()
	if driver == nil {
		return false
	}
	return !v.pausedSettled(driver, ctx)
}

func (v *Visualizer) animationSteps(now time.Time, interval time.Duration) uint64 {
	// Drivers at the normal UI cadence retain the existing one-frame-per-tick
	// behavior. Only faster logical clocks need elapsed-time catch-up.
	if interval <= 0 || interval >= TickFast || now.IsZero() {
		v.resetFrameTiming()
		return 1
	}
	if v.lastFrameTick.IsZero() || v.frameInterval != interval {
		v.lastFrameTick = now
		v.frameElapsed = 0
		v.frameInterval = interval
		return 1
	}

	dt := now.Sub(v.lastFrameTick)
	v.lastFrameTick = now
	if dt <= 0 {
		return 0
	}
	v.frameElapsed += dt
	steps := int(v.frameElapsed / interval)
	if steps > maxAnimationCatchUpSteps {
		steps = maxAnimationCatchUpSteps
		v.frameElapsed %= interval
	} else {
		v.frameElapsed -= time.Duration(steps) * interval
	}
	return uint64(steps)
}

func (v *Visualizer) driverFor(mode VisMode) visModeDriver {
	if v == nil || mode < 0 {
		return nil
	}
	if mode >= VisCount {
		idx := int(mode - VisCount)
		if idx < 0 || idx >= len(v.luaVisNames) {
			return nil
		}
		if driver, ok := v.luaDriverCache[idx]; ok {
			return driver
		}
		driver := &luaModeDriver{index: idx}
		v.luaDriverCache[idx] = driver
		return driver
	}
	if v.drivers[mode] == nil {
		newDriver := visModes[mode].newDriver
		if newDriver == nil {
			return nil
		}
		v.drivers[mode] = newDriver()
	}
	return v.drivers[mode]
}

// columns is the width every mode draws and paces itself at. A visualizer
// that nobody sized has no width, so it neither draws nor ticks.
func (v *Visualizer) columns() int {
	if v == nil || v.Cols < 0 {
		return 0
	}
	return v.Cols
}

func (v *Visualizer) syncDriverMode() visModeDriver {
	if v == nil {
		return nil
	}
	driver := v.driverFor(v.Mode)
	if !v.activeModeSet {
		if driver != nil {
			driver.OnEnter(v)
		}
		v.activeMode = v.Mode
		v.activeModeSet = true
		return driver
	}
	if v.activeMode != v.Mode {
		prev := v.driverFor(v.activeMode)
		prevSpec := VisAnalysisSpec{}
		if prev != nil {
			prevSpec = NormalizeAnalysisSpec(prev.AnalysisSpec(v))
		}
		nextSpec := VisAnalysisSpec{}
		if driver != nil {
			nextSpec = NormalizeAnalysisSpec(driver.AnalysisSpec(v))
		}
		if (prevSpec.BandCount == 0) != (nextSpec.BandCount == 0) {
			v.resetSpectrumHistory()
		}
		v.smoothedBands = v.smoothedBands[:0]
		v.lastSmoothTick = time.Time{}
		if prev != nil {
			prev.OnLeave(v)
		}
		if driver != nil {
			driver.OnEnter(v)
		}
		v.activeMode = v.Mode
	}
	return driver
}
