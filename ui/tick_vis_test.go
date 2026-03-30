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
		Now: time.Now(),
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
