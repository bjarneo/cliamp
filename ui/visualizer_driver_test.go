package ui

import (
	"math"
	"reflect"
	"testing"
	"time"
)

func TestAnalyzeSupportsArbitraryBandCounts(t *testing.T) {
	v := NewVisualizer(44100)
	samples := make([]float64, classicPeakFFTSize)
	for i := range samples {
		samples[i] = math.Sin(2 * math.Pi * 440 * float64(i) / v.sr)
	}

	for _, spec := range []VisAnalysisSpec{
		spectrumAnalysisSpec(DefaultSpectrumBands),
		{BandCount: 17, FFTSize: defaultFFTSize},
		{BandCount: classicPeakSpectrumBands, FFTSize: classicPeakFFTSize},
	} {
		bands := v.Analyze(samples, spec)
		if len(bands) != spec.BandCount {
			t.Fatalf("Analyze(..., %+v) len = %d, want %d", spec, len(bands), spec.BandCount)
		}
	}
}

func TestBuildSpectrumEdgesPreservesLegacyDefaultLayout(t *testing.T) {
	got := buildSpectrumEdges(DefaultSpectrumBands)
	want := legacySpectrumEdges[:]
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("buildSpectrumEdges(%d) = %v, want %v", DefaultSpectrumBands, got, want)
	}
}

func TestAnalyzeDecayStateIsIndependentPerAnalysisSpec(t *testing.T) {
	v := NewVisualizer(44100)
	specA := spectrumAnalysisSpec(DefaultSpectrumBands)
	specB := VisAnalysisSpec{BandCount: DefaultSpectrumBands, FFTSize: classicPeakFFTSize}
	v.prevBySpec[specA] = uniformBandsN(specA.BandCount, 0.5)
	v.prevBySpec[specB] = uniformBandsN(specB.BandCount, 0.8)

	bandsA := v.Analyze(nil, specA)
	bandsB := v.Analyze(nil, specB)

	if got := bandsA[0]; math.Abs(got-0.4) > classicPeakTestEpsilon {
		t.Fatalf("default-spec decay = %v, want 0.4", got)
	}
	if got := bandsB[0]; math.Abs(got-0.64) > classicPeakTestEpsilon {
		t.Fatalf("classic-peak-spec decay = %v, want 0.64", got)
	}
}

func TestAverageSpectrumRangeLinearDistinguishesSubBinLowBands(t *testing.T) {
	magnitudes := make([]float64, classicPeakFFTSize/2)
	for i := range magnitudes {
		magnitudes[i] = float64(i)
	}

	spec := VisAnalysisSpec{BandCount: classicPeakSpectrumBands, FFTSize: classicPeakFFTSize}
	edges := buildSpectrumEdges(spec.BandCount)
	binHz := 44100.0 / float64(spec.FFTSize)
	low := make([]float64, 3)
	for i := range low {
		low[i] = averageSpectrumRangeLinear(magnitudes, edges[i]/binHz, edges[i+1]/binHz)
	}

	if !(low[0] < low[1] && low[1] < low[2]) {
		t.Fatalf("low sub-bin bands collapsed unexpectedly: got %v", low)
	}
}

func TestRenderOnlyDriverUsesDefaultTickInterval(t *testing.T) {
	v := NewVisualizer(44100)
	// Pulse is a per-frame-animated spectrum mode and stays on the default
	// (TickFast) cadence — VisBars/Bricks/Columns/etc. opt into TickAnim.
	activateMode(t, v, VisPulse)

	if got := v.TickInterval(VisTickContext{Playing: true}); got != TickFast {
		t.Fatalf("TickInterval(playing) = %v, want %v", got, TickFast)
	}
	if got := v.TickInterval(VisTickContext{OverlayActive: true}); got != TickSlow {
		t.Fatalf("TickInterval(overlay) = %v, want %v", got, TickSlow)
	}
	if got := v.TickInterval(VisTickContext{}); got != TickSlow {
		t.Fatalf("TickInterval(idle) = %v, want %v", got, TickSlow)
	}
}

func TestSmoothBarsDriverUsesAnimTick(t *testing.T) {
	v := NewVisualizer(44100)
	activateMode(t, v, VisBars)

	if got := v.TickInterval(VisTickContext{Playing: true}); got != TickAnim {
		t.Fatalf("Bars TickInterval(playing) = %v, want %v", got, TickAnim)
	}
}

func TestFastFrameDriverTracksWallClockAtNormalUICadence(t *testing.T) {
	v := NewVisualizer(44100)
	v.Mode = VisScope
	v.Cols = 80
	t0 := time.Unix(1, 0)
	v.Tick(VisTickContext{Now: t0, Playing: true})

	const ticks = 20
	for i := 1; i <= ticks; i++ {
		v.Tick(VisTickContext{Now: t0.Add(time.Duration(i) * TickFast), Playing: true})
	}

	want := uint64(1 + time.Duration(ticks)*TickFast/TickAnim)
	if got := v.Frame(); got != want {
		t.Fatalf("Scope frame after %v = %d, want %d", time.Duration(ticks)*TickFast, got, want)
	}
}

func TestNormalFrameDriverAdvancesOncePerTick(t *testing.T) {
	v := NewVisualizer(44100)
	v.Mode = VisPulse
	v.Cols = 80
	t0 := time.Unix(1, 0)

	for i := range 5 {
		v.Tick(VisTickContext{Now: t0.Add(time.Duration(i) * TickFast), Playing: true})
		if got, want := v.Frame(), uint64(i+1); got != want {
			t.Fatalf("Pulse frame after tick %d = %d, want %d", i+1, got, want)
		}
	}
}

func TestFastFrameDriverBoundsCatchUp(t *testing.T) {
	v := NewVisualizer(44100)
	v.Mode = VisScope
	v.Cols = 80
	t0 := time.Unix(1, 0)
	v.Tick(VisTickContext{Now: t0, Playing: true})
	v.Tick(VisTickContext{Now: t0.Add(time.Second), Playing: true})

	want := uint64(1 + maxAnimationCatchUpSteps)
	if got := v.Frame(); got != want {
		t.Fatalf("Scope frame after stalled tick = %d, want bounded catch-up to %d", got, want)
	}
}

func TestFastFrameDriverDoesNotCatchUpPausedOrHiddenTime(t *testing.T) {
	tests := []struct {
		name    string
		suspend func(*Visualizer, time.Time)
	}{
		{
			name: "paused",
			suspend: func(v *Visualizer, now time.Time) {
				v.Tick(VisTickContext{Now: now, Playing: true, Paused: true})
			},
		},
		{name: "hidden", suspend: func(v *Visualizer, _ time.Time) { v.Suspend() }},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v := NewVisualizer(44100)
			v.Mode = VisScope
			v.Cols = 80
			t0 := time.Unix(1, 0)
			v.Tick(VisTickContext{Now: t0, Playing: true})
			v.Tick(VisTickContext{Now: t0.Add(TickFast), Playing: true})
			before := v.Frame()

			tt.suspend(v, t0.Add(2*TickFast))
			v.Tick(VisTickContext{Now: t0.Add(2 * time.Second), Playing: true})

			if got := v.Frame(); got != before+1 {
				t.Fatalf("Scope frame after resume = %d, want %d", got, before+1)
			}
		})
	}
}

func TestAdvanceSmoothingEasesTowardBands(t *testing.T) {
	v := NewVisualizer(44100)
	v.bands = []float64{1.0, 0.0}
	v.smoothedBands = []float64{0.0, 1.0}

	t0 := time.Unix(0, 0)
	v.lastSmoothTick = t0
	v.advanceSmoothing(t0.Add(TickAnim))

	// Expectations derive from the same easing call the implementation uses,
	// so changes to the rate constants don't silently invalidate the test.
	dt := TickAnim.Seconds()
	wantRise := classicPeakStep(0, 1, dt)
	wantFall := classicPeakStep(1, 0, dt)
	if got := v.smoothedBands[0]; math.Abs(got-wantRise) > 1e-9 {
		t.Fatalf("rise step = %v, want %v", got, wantRise)
	}
	if got := v.smoothedBands[1]; math.Abs(got-wantFall) > 1e-9 {
		t.Fatalf("fall step = %v, want %v", got, wantFall)
	}
	if (1 - wantFall) >= wantRise {
		t.Fatalf("expected decay slower than attack: rise %v, fall %v", wantRise, wantFall)
	}
}

func TestAdvanceSmoothingResizesOnBandCountChange(t *testing.T) {
	v := NewVisualizer(44100)
	v.smoothedBands = []float64{0.5, 0.5}
	v.bands = []float64{0.1, 0.2, 0.3}
	v.advanceSmoothing(time.Time{})

	if len(v.smoothedBands) != len(v.bands) {
		t.Fatalf("smoothedBands len = %d, want %d", len(v.smoothedBands), len(v.bands))
	}
	for i, got := range v.smoothedBands {
		if got != v.bands[i] {
			t.Fatalf("smoothedBands[%d] = %v, want %v after resync", i, got, v.bands[i])
		}
	}
}

func TestPausedBarsDecayToRestThenSuspend(t *testing.T) {
	withPanelWidth(t, 16)

	v := NewVisualizer(44100)
	activateMode(t, v, VisBars)
	v.bands = uniformBands(0.8)
	v.smoothedBands = append([]float64(nil), v.bands...)

	analyze := func(spec VisAnalysisSpec) []float64 {
		return v.Analyze(nil, spec)
	}
	ctxAt := func(now time.Time) VisTickContext {
		return VisTickContext{Now: now, Paused: true, Analyze: analyze}
	}

	prev := append([]float64(nil), v.SmoothedBands()...)
	settled := false
	now := time.Unix(1, 0)
	for i := 0; i < 240; i++ {
		v.Tick(ctxAt(now.Add(time.Duration(i+1) * TickSlow)))
		cur := v.SmoothedBands()
		for b := range len(cur) {
			if cur[b] > prev[b]+1e-9 {
				t.Fatalf("paused tick %d band %d rose %v -> %v, want monotonic decay", i, b, prev[b], cur[b])
			}
		}
		prev = append(prev[:0], cur...)
		if !v.PausedDecayPending(ctxAt(now.Add(time.Duration(i+2) * TickSlow))) {
			settled = true
			break
		}
	}
	if !settled {
		t.Fatal("paused bars never settled to rest")
	}
	for i, got := range prev {
		if got >= pausedDecayEpsilon {
			t.Fatalf("band %d = %v after decay, want below %v", i, got, pausedDecayEpsilon)
		}
	}

	// Once settled, further paused ticks suspend and leave the frame alone.
	frameBefore := v.Frame()
	v.Tick(ctxAt(now.Add(10 * time.Second)))
	if got := v.Frame(); got != frameBefore {
		t.Fatalf("settled paused tick advanced frame %d -> %d", frameBefore, got)
	}
	for i, got := range v.SmoothedBands() {
		if math.Abs(got-prev[i]) > 1e-9 {
			t.Fatalf("settled paused tick changed band %d %v -> %v", i, prev[i], got)
		}
	}
}

func TestDefaultDriverTickGatesAnalyzeAtAnalyzeCadence(t *testing.T) {
	v := NewVisualizer(44100)
	activateMode(t, v, VisBars)

	calls := 0
	analyze := func(spec VisAnalysisSpec) []float64 {
		calls++
		return uniformBandsN(spec.BandCount, 0.4)
	}

	t0 := time.Unix(0, 0)
	// First tick analyzes (no prior timestamp).
	v.Tick(VisTickContext{Now: t0, Playing: true, Analyze: analyze})
	// A tick well within TickAnalyze should NOT analyze again.
	v.Tick(VisTickContext{Now: t0.Add(TickAnim), Playing: true, Analyze: analyze})
	if calls != 1 {
		t.Fatalf("Analyze() calls within analyze window = %d, want 1", calls)
	}
	// Past TickAnalyze, it should analyze again.
	v.Tick(VisTickContext{Now: t0.Add(TickAnalyze + time.Millisecond), Playing: true, Analyze: analyze})
	if calls != 2 {
		t.Fatalf("Analyze() calls after analyze window = %d, want 2", calls)
	}
}

func TestRenderOnlyDriverSkipsAnalyzeUnderOverlay(t *testing.T) {
	v := NewVisualizer(44100)
	activateMode(t, v, VisBars)

	calls := 0
	v.Tick(VisTickContext{
		OverlayActive: true,
		Analyze: func(VisAnalysisSpec) []float64 {
			calls++
			return uniformBands(0.6)
		},
	})

	if calls != 0 {
		t.Fatalf("Analyze() calls = %d, want 0 while overlay is active", calls)
	}
}

func TestRenderOnlyDriverRequestsConfiguredBandCount(t *testing.T) {
	v := NewVisualizer(44100)
	activateMode(t, v, VisBars)

	var requested VisAnalysisSpec
	v.Tick(VisTickContext{
		Analyze: func(spec VisAnalysisSpec) []float64 {
			requested = spec
			return uniformBandsN(spec.BandCount, 0.6)
		},
	})

	want := spectrumAnalysisSpec(DefaultSpectrumBands)
	if requested != want {
		t.Fatalf("Analyze() requested %+v, want %+v", requested, want)
	}
	if len(v.bands) != DefaultSpectrumBands {
		t.Fatalf("stored bands len = %d, want %d", len(v.bands), DefaultSpectrumBands)
	}
}

func TestClassicPeakRequestsHighResBands(t *testing.T) {
	v := NewVisualizer(44100)
	activateMode(t, v, VisClassicPeak)

	var requested VisAnalysisSpec
	v.Tick(VisTickContext{
		Now:     time.Now(),
		Playing: true,
		Analyze: func(spec VisAnalysisSpec) []float64 {
			requested = spec
			return uniformBandsN(spec.BandCount, 0.6)
		},
	})

	want := VisAnalysisSpec{BandCount: classicPeakSpectrumBands, FFTSize: classicPeakFFTSize}
	if requested != want {
		t.Fatalf("Analyze() requested %+v, want %+v", requested, want)
	}
	if len(v.bands) != classicPeakSpectrumBands {
		t.Fatalf("stored bands len = %d, want %d", len(v.bands), classicPeakSpectrumBands)
	}
}

func TestRawSampleModesRefreshWaveBufAtZeroBandCount(t *testing.T) {
	v := NewVisualizer(44100)
	activateMode(t, v, VisWave)

	samples := []float64{-0.5, -0.1, 0.25, 0.75}
	requested := VisAnalysisSpec{BandCount: -1, FFTSize: -1}
	v.Tick(VisTickContext{
		Playing: true,
		Analyze: func(spec VisAnalysisSpec) []float64 {
			requested = spec
			return v.Analyze(samples, spec)
		},
	})

	want := spectrumAnalysisSpec(0)
	if requested != want {
		t.Fatalf("Analyze() requested %+v, want %+v for raw-sample modes", requested, want)
	}
	if !reflect.DeepEqual(v.waveBuf, samples) {
		t.Fatalf("waveBuf = %v, want %v after zero-band tick refresh", v.waveBuf, samples)
	}
}

func TestRawSampleModesClearSpectrumHistoryOnModeSwitch(t *testing.T) {
	v := NewVisualizer(44100)
	barsSpec := spectrumAnalysisSpec(DefaultSpectrumBands)
	v.prevBySpec[barsSpec] = uniformBandsN(barsSpec.BandCount, 0.8)

	activateMode(t, v, VisBars)
	activateMode(t, v, VisWave)

	if len(v.prevBySpec) != 0 {
		t.Fatalf("prevBySpec len after switch to raw-sample mode = %d, want 0", len(v.prevBySpec))
	}

	activateMode(t, v, VisBars)
	bands := v.Analyze(nil, barsSpec)
	if got := bands[0]; math.Abs(got) > classicPeakTestEpsilon {
		t.Fatalf("first bar after raw-sample switch = %v, want 0 with cleared spectrum history", got)
	}
}

func TestTerrainPreservesStateAcrossModeSwitch(t *testing.T) {
	withPanelWidth(t, 8)

	v := NewVisualizer(44100)
	activateMode(t, v, VisTerrain)
	driver := terrainDriverFor(t, v)
	bands := uniformBands(0.6)
	v.bands = bands

	v.Tick(VisTickContext{})
	snapshot := append([]float64(nil), driver.buf...)
	if len(snapshot) != PanelWidth*2 {
		t.Fatalf("terrain buffer len = %d, want %d", len(snapshot), PanelWidth*2)
	}

	activateMode(t, v, VisBars)
	activateMode(t, v, VisTerrain)

	if len(driver.buf) != len(snapshot) {
		t.Fatalf("terrain buffer len after switch = %d, want %d", len(driver.buf), len(snapshot))
	}
	for i, got := range driver.buf {
		if got != snapshot[i] {
			t.Fatalf("terrain buffer[%d] = %v after switch, want %v", i, got, snapshot[i])
		}
	}
}

func TestTerrainRenderDoesNotAdvanceWithoutTick(t *testing.T) {
	withPanelWidth(t, 8)

	v := NewVisualizer(44100)
	activateMode(t, v, VisTerrain)
	driver := terrainDriverFor(t, v)
	v.bands = uniformBands(0.6)

	v.Tick(VisTickContext{})
	snapshot := append([]float64(nil), driver.buf...)

	v.Render()
	v.Render()

	if !reflect.DeepEqual(driver.buf, snapshot) {
		t.Fatalf("terrain buffer changed across redraws without tick: got %v want %v", driver.buf, snapshot)
	}
}

func TestTerrainTickSkipsAnalyzeUnderOverlay(t *testing.T) {
	withPanelWidth(t, 8)

	v := NewVisualizer(44100)
	activateMode(t, v, VisTerrain)
	driver := terrainDriverFor(t, v)
	driver.buf = append([]float64(nil), []float64{
		0.1, 0.2, 0.3, 0.4,
		0.5, 0.6, 0.7, 0.8,
		0.2, 0.3, 0.4, 0.5,
		0.6, 0.7, 0.8, 0.9,
	}...)
	snapshot := append([]float64(nil), driver.buf...)

	calls := 0
	v.Tick(VisTickContext{
		OverlayActive: true,
		Analyze: func(VisAnalysisSpec) []float64 {
			calls++
			return uniformBands(0.6)
		},
	})

	if calls != 0 {
		t.Fatalf("Analyze() calls = %d, want 0 while overlay is active", calls)
	}
	if !reflect.DeepEqual(driver.buf, snapshot) {
		t.Fatalf("terrain buffer changed under overlay: got %v want %v", driver.buf, snapshot)
	}
}
