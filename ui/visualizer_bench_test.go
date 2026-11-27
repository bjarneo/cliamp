package ui

import (
	"math"
	"math/rand/v2"
	"strings"
	"testing"
	"time"
)

// benchSampleRate matches the default mixer sample rate used elsewhere in the package.
const benchSampleRate = 44100

// benchSamplesSine fills a buffer with a 440 Hz sine wave — a clean, single-tone
// workload that exercises the FFT without triggering any silence short-circuit.
func benchSamplesSine(n int) []float64 {
	buf := make([]float64, n)
	for i := range buf {
		buf[i] = math.Sin(2 * math.Pi * 440 * float64(i) / float64(benchSampleRate))
	}
	return buf
}

// benchSamplesNoise fills a buffer with deterministic white noise — the worst
// case for per-band averaging (energy spread across every bin).
func benchSamplesNoise(n int) []float64 {
	r := rand.New(rand.NewPCG(1, 2))
	buf := make([]float64, n)
	for i := range buf {
		buf[i] = r.Float64()*2 - 1
	}
	return buf
}

// benchSamplesSilence returns a zeroed buffer — the common case between tracks
// or during pause, where ideally we'd skip most of the FFT pipeline.
func benchSamplesSilence(n int) []float64 {
	return make([]float64, n)
}

func BenchmarkAnalyze(b *testing.B) {
	spec := spectrumAnalysisSpec(DefaultSpectrumBands)
	cases := []struct {
		name    string
		samples []float64
	}{
		{"Sine440", benchSamplesSine(defaultFFTSize)},
		{"Noise", benchSamplesNoise(defaultFFTSize)},
		{"Silence", benchSamplesSilence(defaultFFTSize)},
	}
	for _, tc := range cases {
		b.Run(tc.name, func(b *testing.B) {
			v := NewVisualizer(benchSampleRate)
			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				v.Analyze(tc.samples, spec)
			}
		})
	}
}

// benchDriverModes covers the representative rendering shapes: pure spectrum
// bars, peak-cap animation, frame-driven animation, waveform oscilloscope.
var benchDriverModes = []VisMode{
	VisBars,
	VisBarsDot,
	VisBarsOutline,
	VisClassicPeak,
	VisMatrix,
	VisRain,
	VisFlame,
	VisLogo,
	VisPulse,
	VisMirror,
	VisWave,
	VisScope,
}

// benchFullscreenModes lists the heavy modes covered at 200x40 fullscreen size.
var benchFullscreenModes = []VisMode{
	VisBars,
	VisWave,
	VisMatrix,
	VisFlame,
	VisClassicPeak,
	VisClassicLED,
	VisOmarchy,
	VisEthereal,
	VisYinYang,
}

func benchRenderMode(b *testing.B, mode VisMode, cols, rows int) {
	samples := benchSamplesSine(classicPeakFFTSize)
	v := NewVisualizer(benchSampleRate)
	v.Mode = mode
	v.Cols = cols
	v.Rows = rows
	now := time.Unix(1_700_000_000, 0)
	ctx := VisTickContext{
		Now:     now,
		Playing: true,
		Analyze: func(spec VisAnalysisSpec) []float64 {
			spec = NormalizeAnalysisSpec(spec)
			return v.Analyze(samples[:min(len(samples), spec.FFTSize)], spec)
		},
	}
	for range 4 {
		ctx.Now = ctx.Now.Add(TickFast)
		v.Tick(ctx)
	}
	if v.Render() == "" {
		b.Fatal("Render() returned empty output")
	}
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		ctx.Now = ctx.Now.Add(TickFast)
		_ = v.Render()
	}
}

func benchTickPipelineMode(b *testing.B, mode VisMode, cols, rows int) {
	samples := benchSamplesSine(classicPeakFFTSize)
	v := NewVisualizer(benchSampleRate)
	v.Mode = mode
	v.Cols = cols
	v.Rows = rows
	now := time.Unix(1_700_000_000, 0)
	ctx := VisTickContext{
		Now:     now,
		Playing: true,
		Analyze: func(spec VisAnalysisSpec) []float64 {
			spec = NormalizeAnalysisSpec(spec)
			return v.Analyze(samples[:min(len(samples), spec.FFTSize)], spec)
		},
	}
	ctx.Now = ctx.Now.Add(TickFast)
	v.Tick(ctx)
	if v.Render() == "" {
		b.Fatal("Render() returned empty output")
	}
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		ctx.Now = ctx.Now.Add(TickFast)
		v.Tick(ctx)
		_ = v.Render()
	}
}

func BenchmarkRender(b *testing.B) {
	for _, mode := range benchDriverModes {
		name := visModes[mode].name
		b.Run(name, func(b *testing.B) {
			benchRenderMode(b, mode, 80, 5)
		})
	}
	for _, mode := range benchFullscreenModes {
		name := visModes[mode].name
		b.Run("200x40/"+name, func(b *testing.B) {
			benchRenderMode(b, mode, 200, 40)
		})
	}
}

func BenchmarkTickPipeline(b *testing.B) {
	for _, mode := range benchDriverModes {
		name := visModes[mode].name
		b.Run(name, func(b *testing.B) {
			benchTickPipelineMode(b, mode, 80, 5)
		})
	}
	for _, mode := range benchFullscreenModes {
		name := visModes[mode].name
		b.Run("200x40/"+name, func(b *testing.B) {
			benchTickPipelineMode(b, mode, 200, 40)
		})
	}
}

func BenchmarkFitVisualizerFrame(b *testing.B) {
	line := "\x1b[38;2;255;128;0m" + strings.Repeat("界 visualizer ", 12) + "\x1b[0m"
	frame := strings.Repeat(line+"\n", DefaultVisRows-1) + line
	b.ReportAllocs()
	for b.Loop() {
		_ = fitVisualizerFrame(frame, 76, DefaultVisRows)
	}
}

func BenchmarkVisualizerFrameAccounting(b *testing.B) {
	v := NewVisualizer(benchSampleRate)
	v.Mode = VisScope
	v.Cols = 80
	now := time.Unix(1, 0)
	ctx := VisTickContext{Now: now, Playing: true}
	v.Tick(ctx)
	b.ReportAllocs()
	for b.Loop() {
		now = now.Add(TickFast)
		ctx.Now = now
		v.Tick(ctx)
	}
}
