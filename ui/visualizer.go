package ui

import (
	"math"
	"math/cmplx"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/madelynnblue/go-dsp/fft"
)

const (
	numBands       = 10
	fftSize        = 2048
	defaultVisRows = 5
)

// VisMode selects the visualizer rendering style.
type VisMode int

const (
	VisBars    VisMode = iota // smooth fractional blocks
	VisBricks                 // solid bricks with gaps
	VisColumns                // many thin columns
	VisWave                   // braille waveform oscilloscope
	VisScatter                // braille particle sparkle
	VisFlame                  // braille rising flame tendrils
	VisRetro                  // 80s synthwave perspective grid with wave
	VisPulse                  // braille pulsating circle
	VisNone                   // hidden — no visualizer
	visCount                  // sentinel for cycling
)

// Unicode block elements for bar height (9 levels including space)
var barBlocks = []string{" ", "▁", "▂", "▃", "▄", "▅", "▆", "▇", "█"}

// brailleBit maps (row, col) in a 4×2 Braille dot grid to its bit value.
var brailleBit = [4][2]rune{
	{0x01, 0x08}, // row 0
	{0x02, 0x10}, // row 1
	{0x04, 0x20}, // row 2
	{0x40, 0x80}, // row 3
}

// visBandWidth returns the character width for band b so that all 10 bands
// plus 1-char gaps exactly fill panelWidth (74). The remainder is distributed
// across the first few bands (7 chars) while the rest get 6.
func visBandWidth(b int) int {
	const gap = 1
	base := (panelWidth - (numBands-1)*gap) / numBands
	extra := (panelWidth - (numBands-1)*gap) % numBands
	if b < extra {
		return base + 1
	}
	return base
}

// Frequency edges for 10 spectrum bands (Hz)
var bandEdges = [11]float64{20, 100, 200, 400, 800, 1600, 3200, 6400, 12800, 16000, 20000}

// Pre-built styles for spectrum bar colors to avoid per-frame allocation.
var (
	specLowStyle  = lipgloss.NewStyle().Foreground(spectrumLow)
	specMidStyle  = lipgloss.NewStyle().Foreground(spectrumMid)
	specHighStyle = lipgloss.NewStyle().Foreground(spectrumHigh)
)

// Visualizer performs FFT analysis and renders spectrum bars.
type Visualizer struct {
	prev    [numBands]float64 // previous frame for temporal smoothing
	sr      float64
	buf     []float64 // reusable FFT buffer to avoid per-frame allocation
	Mode    VisMode
	Rows    int       // display height in terminal rows (default 5)
	waveBuf []float64 // raw samples for wave mode
	frame   uint64    // frame counter for scatter animation
}

// NewVisualizer creates a Visualizer for the given sample rate.
func NewVisualizer(sampleRate float64) *Visualizer {
	return &Visualizer{
		sr:   sampleRate,
		buf:  make([]float64, fftSize),
		Rows: defaultVisRows,
	}
}

// CycleMode advances to the next visualizer mode.
func (v *Visualizer) CycleMode() {
	v.Mode = (v.Mode + 1) % visCount
}

// ModeName returns the display name of the current mode.
func (v *Visualizer) ModeName() string {
	switch v.Mode {
	case VisBricks:
		return "Bricks"
	case VisColumns:
		return "Columns"
	case VisWave:
		return "Wave"
	case VisScatter:
		return "Scatter"
	case VisFlame:
		return "Flame"
	case VisRetro:
		return "Retro"
	case VisPulse:
		return "Pulse"
	case VisNone:
		return "None"
	default:
		return "Bars"
	}
}

// StringToVisMode converts a visualizer mode name (case-insensitive) to VisMode.
// Returns VisBars (default) if the name is not recognized or empty.
func StringToVisMode(name string) VisMode {
	if name == "" {
		return VisBars
	}
	switch strings.ToLower(name) {
	case "bars":
		return VisBars
	case "bricks":
		return VisBricks
	case "columns":
		return VisColumns
	case "wave":
		return VisWave
	case "scatter":
		return VisScatter
	case "flame":
		return VisFlame
	case "retro":
		return VisRetro
	case "pulse":
		return VisPulse
	case "none":
		return VisNone
	default:
		return VisBars
	}
}

// Analyze runs FFT on raw audio samples and returns 10 normalized band levels (0-1).
func (v *Visualizer) Analyze(samples []float64) [numBands]float64 {
	v.frame++

	// Store raw samples for wave mode.
	if n := len(samples); n > 0 {
		if cap(v.waveBuf) >= n {
			v.waveBuf = v.waveBuf[:n]
		} else {
			v.waveBuf = make([]float64, n)
		}
		copy(v.waveBuf, samples)
	} else {
		v.waveBuf = v.waveBuf[:0]
	}

	var bands [numBands]float64
	if len(samples) == 0 {
		// Decay previous values when no audio data
		for b := range numBands {
			bands[b] = v.prev[b] * 0.8
			v.prev[b] = bands[b]
		}
		return bands
	}

	// Zero-fill and copy into reusable buffer
	clear(v.buf)
	copy(v.buf, samples)

	// Apply Hann window to reduce spectral leakage
	for i := range fftSize {
		w := 0.5 * (1 - math.Cos(2*math.Pi*float64(i)/float64(fftSize-1)))
		v.buf[i] *= w
	}

	// Compute FFT
	spectrum := fft.FFTReal(v.buf)
	halfLen := len(spectrum) / 2

	binHz := v.sr / float64(fftSize)

	// Sum magnitudes per frequency band
	for b := range numBands {
		loIdx := int(bandEdges[b] / binHz)
		hiIdx := int(bandEdges[b+1] / binHz)
		if loIdx < 1 {
			loIdx = 1
		}
		if hiIdx >= halfLen {
			hiIdx = halfLen - 1
		}

		var sum float64
		count := 0
		for i := loIdx; i <= hiIdx; i++ {
			sum += cmplx.Abs(spectrum[i])
			count++
		}
		if count > 0 {
			sum /= float64(count)
		}

		// Convert to dB-like scale and normalize to 0-1
		if sum > 0 {
			bands[b] = (20*math.Log10(sum) + 10) / 50
		}
		bands[b] = max(0, min(1, bands[b]))

		// Temporal smoothing: fast attack, slow decay
		if bands[b] > v.prev[b] {
			bands[b] = bands[b]*0.6 + v.prev[b]*0.4
		} else {
			bands[b] = bands[b]*0.25 + v.prev[b]*0.75
		}
		v.prev[b] = bands[b]
	}

	return bands
}

// Render dispatches to the active visualizer mode.
func (v *Visualizer) Render(bands [numBands]float64) string {
	switch v.Mode {
	case VisBricks:
		return v.renderBricks(bands)
	case VisColumns:
		return v.renderColumns(bands)
	case VisWave:
		return v.renderWave()
	case VisScatter:
		return v.renderScatter(bands)
	case VisFlame:
		return v.renderFlame(bands)
	case VisRetro:
		return v.renderRetro(bands)
	case VisPulse:
		return v.renderPulse(bands)
	case VisNone:
		return ""
	default:
		return v.renderBars(bands)
	}
}

// specStyle returns the spectrum color style for a given row height (0-1).
func specStyle(rowBottom float64) lipgloss.Style {
	switch {
	case rowBottom >= 0.6:
		return specHighStyle
	case rowBottom >= 0.3:
		return specMidStyle
	default:
		return specLowStyle
	}
}

// scatterHash returns a pseudo-random value in [0, 1) for a given dot position
// and frame. Dots persist for a few frames to create a twinkling effect.
func scatterHash(band, row, col int, frame uint64) float64 {
	// Stagger per-dot so they don't all change simultaneously.
	f := (frame + uint64(row*3+col)) / 3
	h := uint64(band)*7919 + uint64(row)*6271 + uint64(col)*3037 + f*104729
	h ^= h >> 16
	h *= 0x45d9f3b37197344b
	h ^= h >> 16
	return float64(h%10000) / 10000.0
}
