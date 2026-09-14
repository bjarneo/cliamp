package ui

import (
	"math"
	"testing"
	"time"
)

// Moving the FFT window across an unchanged bass tone must not make it pulse.
// This exercises real analysis plus motion, rather than injecting fixed bands.
func TestClassicPeakSteadyBassDoesNotFlicker(t *testing.T) {
	withPanelWidth(t, 40)
	for _, hz := range []float64{41.2, 55, 82.4, 110} {
		for _, interval := range []time.Duration{16 * time.Millisecond, 32 * time.Millisecond, 50 * time.Millisecond} {
			v := NewVisualizer(44100)
			activateMode(t, v, VisClassicPeak)
			d := classicPeakDriverFor(t, v)
			spec := d.AnalysisSpec(v)
			samples := make([]float64, spec.FFTSize)
			var previous []float64
			now := time.Unix(1, 0)
			for frame := range 100 {
				offset := float64(frame) * interval.Seconds() * v.sr
				for i := range samples {
					samples[i] = 0.6 * math.Sin(2*math.Pi*hz*(offset+float64(i))/v.sr)
				}
				d.Tick(v, VisTickContext{
					Now: now.Add(time.Duration(frame) * interval), Playing: true,
					Analyze: func(spec VisAnalysisSpec) []float64 { return v.Analyze(samples, spec) },
				})
				if frame > 20 {
					for col, level := range d.barPos {
						if jump := math.Abs(level - previous[col]); jump > 0.025 {
							t.Fatalf("%g Hz at %v: bar %d jumped %.3f on a steady tone", hz, interval, col, jump)
						}
					}
				}
				previous = append(previous[:0], d.barPos...)
			}
		}
	}
}
