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

// hannWindow is the precomputed Hann window for the FFT. Computing it once
// avoids 2048 math.Cos calls per frame.
var hannWindow [fftSize]float64

// VisMode selects the visualizer rendering style.
type VisMode int

const (
	VisBars        VisMode = iota // smooth fractional blocks
	VisBarsDot                    // bars with braille dot stipple
	VisRain                       // falling rain droplets within bar shapes
	VisBarsOutline                // top-edge outline of bars
	VisBricks                     // solid bricks with gaps
	VisColumns                // many thin columns
	VisWave                   // braille waveform oscilloscope
	VisScatter                // braille particle sparkle
	VisFlame                  // braille rising flame tendrils
	VisRetro                  // 80s synthwave perspective grid with wave
	VisPulse                  // braille pulsating circle
	VisMatrix                 // falling matrix rain characters
	VisBinary                 // streaming binary 0s and 1s
	VisSakura                 // falling cherry blossom petals
	VisFirework               // exploding firework bursts
	VisLogo                   // CLIAMP pixel text
	VisTerrain                // scrolling side-view mountain range
	VisGlitch                 // random block corruption driven by energy
	VisScope                  // Lissajous XY oscilloscope
	VisHeartbeat              // ECG pulse monitor trace
	VisButterfly              // mirrored Rorschach spectrum
	VisLightning              // electric bolts from treble energy
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
	prev      [numBands]float64 // previous frame for temporal smoothing
	sr        float64
	buf       []float64 // reusable FFT buffer to avoid per-frame allocation
	Mode      VisMode
	Rows      int       // display height in terminal rows (default 5)
	waveBuf   []float64 // raw samples for wave mode
	frame      uint64    // frame counter for scatter animation
	sampleBuf  []float64 // reusable buffer for reading audio tap samples
	terrainBuf []float64 // height history for terrain scrolling mode

	// Lua plugin visualizers (modes numbered visCount, visCount+1, ...)
	luaVisNames []string    // Lua visualizer names in order
	luaRender   luaVisRenderer // callback for Lua visualizer rendering
}

// luaVisRenderer is the callback type for rendering a Lua visualizer frame.
type luaVisRenderer func(name string, bands [10]float64, rows, cols int, frame uint64) string

// NewVisualizer creates a Visualizer for the given sample rate.
func NewVisualizer(sampleRate float64) *Visualizer {
	return &Visualizer{
		sr:        sampleRate,
		buf:       make([]float64, fftSize),
		sampleBuf: make([]float64, fftSize),
		Rows:      defaultVisRows,
	}
}

// CycleMode advances to the next visualizer mode, including Lua visualizers.
func (v *Visualizer) CycleMode() {
	total := visCount + VisMode(len(v.luaVisNames))
	v.Mode = (v.Mode + 1) % total
}

// visEntry pairs a display name with a render function for a visualizer mode.
type visEntry struct {
	name   string
	render func(*Visualizer, [numBands]float64) string
}

// visModes is the single source of truth for all visualizer modes.
// To add a new mode: add a const, add one line here, create a vis_*.go file.
var visModes = [visCount]visEntry{
	VisBars:        {"Bars", (*Visualizer).renderBars},
	VisBarsDot:     {"BarsDot", (*Visualizer).renderBarsDot},
	VisRain:        {"Rain", (*Visualizer).renderRain},
	VisBarsOutline: {"BarsOutline", (*Visualizer).renderBarsOutline},
	VisBricks:      {"Bricks", (*Visualizer).renderBricks},
	VisColumns: {"Columns", (*Visualizer).renderColumns},
	VisWave:    {"Wave", func(v *Visualizer, _ [numBands]float64) string { return v.renderWave() }},
	VisScatter: {"Scatter", (*Visualizer).renderScatter},
	VisFlame:   {"Flame", (*Visualizer).renderFlame},
	VisRetro:   {"Retro", (*Visualizer).renderRetro},
	VisPulse:   {"Pulse", (*Visualizer).renderPulse},
	VisMatrix:  {"Matrix", (*Visualizer).renderMatrix},
	VisBinary:  {"Binary", (*Visualizer).renderBinary},
	VisSakura:   {"Sakura", (*Visualizer).renderSakura},
	VisFirework: {"Firework", (*Visualizer).renderFirework},
	VisLogo:     {"Logo", (*Visualizer).renderLogo},
	VisTerrain:  {"Terrain", (*Visualizer).renderTerrain},
	VisGlitch:   {"Glitch", (*Visualizer).renderGlitch},
	VisScope:           {"Scope", func(v *Visualizer, _ [numBands]float64) string { return v.renderScope() }},
	VisHeartbeat:       {"Heartbeat", func(v *Visualizer, _ [numBands]float64) string { return v.renderHeartbeat() }},
	VisButterfly:       {"Butterfly", (*Visualizer).renderButterfly},
	VisLightning:       {"Lightning", (*Visualizer).renderLightning},
	VisNone:            {"None", nil},
}

var visNameMap map[string]VisMode

func init() {
	visNameMap = make(map[string]VisMode, visCount)
	for i := range visCount {
		visNameMap[strings.ToLower(visModes[i].name)] = VisMode(i)
	}
	for i := range fftSize {
		hannWindow[i] = 0.5 * (1 - math.Cos(2*math.Pi*float64(i)/float64(fftSize-1)))
	}
}

// ModeName returns the display name of the current mode.
func (v *Visualizer) ModeName() string {
	if v.Mode < visCount {
		return visModes[v.Mode].name
	}
	luaIdx := int(v.Mode - visCount)
	if luaIdx < len(v.luaVisNames) {
		return v.luaVisNames[luaIdx]
	}
	return "Unknown"
}

// StringToVisMode converts a visualizer mode name (case-insensitive) to VisMode.
// Returns VisBars (default) if the name is not recognized or empty.
func StringToVisMode(name string) VisMode {
	if mode, ok := visNameMap[strings.ToLower(name)]; ok {
		return mode
	}
	return VisBars
}

// RegisterLuaVisualizers adds Lua visualizer names so they can be cycled
// through with the v key. renderer is called when a Lua visualizer is active.
func (v *Visualizer) RegisterLuaVisualizers(names []string, renderer luaVisRenderer) {
	v.luaVisNames = names
	v.luaRender = renderer
	// Add to name map for StringToVisMode lookups.
	for i, name := range names {
		visNameMap[strings.ToLower(name)] = visCount + VisMode(i)
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

	// Apply precomputed Hann window to reduce spectral leakage.
	for i := range fftSize {
		v.buf[i] *= hannWindow[i]
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
	if v.Mode < visCount {
		if render := visModes[v.Mode].render; render != nil {
			return render(v, bands)
		}
		return ""
	}
	// Lua visualizer mode.
	luaIdx := int(v.Mode - visCount)
	if luaIdx < len(v.luaVisNames) && v.luaRender != nil {
		return v.luaRender(v.luaVisNames[luaIdx], bands, v.Rows, panelWidth, v.frame)
	}
	return ""
}

// fracBlock returns the fractional Unicode block character for a band level
// within the row span [rowBottom, rowTop]. Used by bars and columns visualizers.
func fracBlock(level, rowBottom, rowTop float64) string {
	if level >= rowTop {
		return "█"
	}
	if level > rowBottom {
		frac := (level - rowBottom) / (rowTop - rowBottom)
		idx := int(frac * float64(len(barBlocks)-1))
		idx = max(0, min(idx, len(barBlocks)-1))
		return barBlocks[idx]
	}
	return " "
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

// specTag returns 0, 1, or 2 identifying the spectrum color tier for style-run
// batching. Mirrors the thresholds in specStyle.
func specTag(norm float64) int {
	if norm >= 0.6 {
		return 2
	}
	if norm >= 0.3 {
		return 1
	}
	return 0
}

// flushStyleRun renders accumulated text in run with the spectrum style for the
// given tag, appends to sb, and resets run. Tag -1 writes unstyled text.
func flushStyleRun(sb *strings.Builder, run *strings.Builder, tag int) {
	if run.Len() == 0 {
		return
	}
	s := run.String()
	switch tag {
	case 2:
		sb.WriteString(specHighStyle.Render(s))
	case 1:
		sb.WriteString(specMidStyle.Render(s))
	case 0:
		sb.WriteString(specLowStyle.Render(s))
	default:
		sb.WriteString(s)
	}
	run.Reset()
}
