package ui

import (
	"testing"
	"time"
)

func TestTickIntervalClassicPeakSettlingUsesAdaptiveCadence(t *testing.T) {
	v := NewVisualizer(44100)
	activateMode(t, v, VisClassicPeak)
	driver := classicPeakDriverFor(t, v)
	v.Rows = DefaultVisRows
	v.bands = uniformBands(0.3)
	driver.barPos = repeatedClassicPeakSlice(8, 0.3)
	driver.peakPos = repeatedClassicPeakSlice(8, 0.5)
	driver.peakVel = repeatedClassicPeakSlice(8, 0)

	withPanelWidth(t, 8)

	if !driver.animating(v) {
		t.Fatal("animating() = false, want true while ClassicPeak caps are still settling")
	}

	wantFPS := classicPeakLaunchMax * float64(DefaultVisRows*len(classicPeakGlyphs))
	wantFPS = min(classicPeakMaxFPS, max(classicPeakMinFPS, wantFPS))
	want := time.Duration(float64(time.Second) / wantFPS)

	ctx := VisTickContext{}
	if got := v.TickInterval(ctx); got != want {
		t.Fatalf("TickInterval() = %v, want %v while ClassicPeak caps are still settling", got, want)
	}
}

func TestClassicPeakAnalysisIntervalUsesFFTOverlapLimit(t *testing.T) {
	v := NewVisualizer(44100)
	activateMode(t, v, VisClassicPeak)
	driver := classicPeakDriverFor(t, v)
	v.Rows = 24

	frame := driver.frameInterval(v)
	if frame != tickClassicPeak {
		t.Fatalf("frameInterval() = %v, want %v when rows clamp to max FPS", frame, tickClassicPeak)
	}

	spec := driver.AnalysisSpec(v)
	window := time.Duration(float64(time.Second) * float64(spec.FFTSize) / v.sr)
	want := max(frame, max(classicPeakSampleFloor, time.Duration(float64(window)/classicPeakFFTOverlap)))
	if got := driver.analysisInterval(v); got != want {
		t.Fatalf("analysisInterval() = %v, want %v", got, want)
	}
}

func TestClassicPeakAnalysisCadenceKeepsItsPhase(t *testing.T) {
	withPanelWidth(t, 80)

	v := NewVisualizer(44100)
	activateMode(t, v, VisClassicPeak)
	driver := classicPeakDriverFor(t, v)
	frame := driver.frameInterval(v)
	analysis := driver.analysisInterval(v)
	start := time.Unix(1, 0)
	calls := 0
	var elapsed time.Duration

	for elapsed = 0; elapsed <= time.Second; elapsed += frame {
		driver.Tick(v, VisTickContext{
			Now:     start.Add(elapsed),
			Playing: true,
			Analyze: func(VisAnalysisSpec) []float64 {
				calls++
				return uniformBandsN(classicPeakSpectrumBands, 0.2)
			},
		})
	}

	lastTick := elapsed - frame
	want := 1 + int(lastTick/analysis)
	if calls != want {
		t.Fatalf("Analyze() calls over %v = %d, want %d", lastTick, calls, want)
	}
}

func TestClassicPeakAnalysisCadenceKeepsSampleFloor(t *testing.T) {
	withPanelWidth(t, 80)

	v := NewVisualizer(96000)
	v.Rows = 24
	activateMode(t, v, VisClassicPeak)
	driver := classicPeakDriverFor(t, v)
	frame := driver.frameInterval(v)
	start := time.Unix(1, 0)
	var analyzedAt []time.Time

	for elapsed := time.Duration(0); elapsed <= time.Second; elapsed += frame {
		now := start.Add(elapsed)
		driver.Tick(v, VisTickContext{
			Now:     now,
			Playing: true,
			Analyze: func(VisAnalysisSpec) []float64 {
				analyzedAt = append(analyzedAt, now)
				return uniformBandsN(classicPeakSpectrumBands, 0.2)
			},
		})
	}

	if len(analyzedAt) < 2 {
		t.Fatalf("Analyze() calls = %d, want at least 2", len(analyzedAt))
	}
	for i := 1; i < len(analyzedAt); i++ {
		if gap := analyzedAt[i].Sub(analyzedAt[i-1]); gap < classicPeakSampleFloor {
			t.Fatalf("analysis gap = %v, want at least %v", gap, classicPeakSampleFloor)
		}
	}
}

func TestClassicPeakAnalysisFloorSurvivesIntervalChange(t *testing.T) {
	withPanelWidth(t, 80)

	v := NewVisualizer(192000)
	activateMode(t, v, VisClassicPeak)
	v.Rows = 5
	driver := classicPeakDriverFor(t, v)
	var analyzedAt []time.Time
	tick := func(now time.Time) {
		driver.Tick(v, VisTickContext{
			Now:     now,
			Playing: true,
			Analyze: func(VisAnalysisSpec) []float64 {
				analyzedAt = append(analyzedAt, now)
				return uniformBandsN(classicPeakSpectrumBands, 0.2)
			},
		})
	}

	// A late tick leaves the hop phase behind the real analysis time; growing
	// the panel then shortens the analysis interval before the next hop.
	start := time.Unix(1, 0)
	tick(start)
	tick(start.Add(40 * time.Millisecond))
	v.Rows = 10
	tick(start.Add(52 * time.Millisecond))
	tick(start.Add(60 * time.Millisecond))

	if len(analyzedAt) != 3 {
		t.Fatalf("Analyze() calls = %d, want 3", len(analyzedAt))
	}
	for i := 1; i < len(analyzedAt); i++ {
		if gap := analyzedAt[i].Sub(analyzedAt[i-1]); gap < classicPeakSampleFloor {
			t.Fatalf("analysis gap = %v after interval change, want at least %v", gap, classicPeakSampleFloor)
		}
	}
}

func TestTickClassicPeakStoppedDecayKeepsAnimatingTowardSilence(t *testing.T) {
	v := NewVisualizer(44100)
	activateMode(t, v, VisClassicPeak)
	driver := classicPeakDriverFor(t, v)
	v.Rows = DefaultVisRows
	spec := driver.AnalysisSpec(v)
	v.prevBySpec[spec] = uniformBandsN(spec.BandCount, 0.6)
	v.bands = uniformBandsN(spec.BandCount, 0.6)
	driver.barPos = repeatedClassicPeakSlice(8, 0.6)
	driver.peakPos = repeatedClassicPeakSlice(8, 0.6)
	driver.peakVel = repeatedClassicPeakSlice(8, 0)
	driver.peakHold = repeatedClassicPeakSlice(8, 0)

	withPanelWidth(t, 8)

	calls := 0
	driver.Tick(v, VisTickContext{
		Now: time.Unix(1, 0),
		Analyze: func(VisAnalysisSpec) []float64 {
			calls++
			return uniformBands(1)
		},
	})

	if calls != 0 {
		t.Fatalf("Analyze() calls = %d, want 0 while stopped decay runs toward silence", calls)
	}
	if got := v.bands[0]; got >= 0.6 {
		t.Fatalf("tickClassicPeak() kept stopped band at %v, want decay below 0.6", got)
	}
	if !driver.animating(v) {
		t.Fatal("animating() = false after stopped decay, want true while bars settle toward silence")
	}
}
