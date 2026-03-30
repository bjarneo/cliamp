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
	activateMode(t, v, VisBars)

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
