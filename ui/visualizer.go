package ui

import (
	"math"
	"math/cmplx"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/madelynnblue/go-dsp/fft"
)

const (
	defaultSpectrumBands = 10
	defaultFFTSize       = 2048
	defaultVisRows       = 5
	minSpectrumHz        = 20.0
	maxSpectrumHz        = 20000.0
)

var legacySpectrumEdges = [defaultSpectrumBands + 1]float64{
	minSpectrumHz,
	100,
	200,
	400,
	800,
	1600,
	3200,
	6400,
	12800,
	16000,
	maxSpectrumHz,
}

// VisMode selects the visualizer rendering style.
type VisMode int

const (
	VisBars        VisMode = iota // smooth fractional blocks
	VisBarsDot                    // bars with braille dot stipple
	VisRain                       // falling rain droplets within bar shapes
	VisBarsOutline                // top-edge outline of bars
	VisBricks                     // solid bricks with gaps
	VisColumns                    // many thin columns
	VisClassicPeak                // classic falling peak caps over thin columns
	VisWave                       // braille waveform oscilloscope
	VisScatter                    // braille particle sparkle
	VisFlame                      // braille rising flame tendrils
	VisRetro                      // 80s synthwave perspective grid with wave
	VisPulse                      // braille pulsating circle
	VisMatrix                     // falling matrix rain characters
	VisBinary                     // streaming binary 0s and 1s
	VisSakura                     // falling cherry blossom petals
	VisFirework                   // exploding firework bursts
	VisLogo                       // CLIAMP pixel text
	VisTerrain                    // scrolling side-view mountain range
	VisGlitch                     // random block corruption driven by energy
	VisScope                      // Lissajous XY oscilloscope
	VisHeartbeat                  // ECG pulse monitor trace
	VisButterfly                  // mirrored Rorschach spectrum
	VisLightning                  // electric bolts from treble energy
	VisNone                       // hidden — no visualizer
	visCount                      // sentinel for cycling
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

// visBandWidth returns the character width for band b so that all bands plus
// 1-char gaps exactly fill panelWidth. The remainder is distributed across the
// first few bands.
func visBandWidth(totalBands, b int) int {
	const gap = 1
	if totalBands <= 0 {
		return 0
	}
	base := (panelWidth - (totalBands-1)*gap) / totalBands
	extra := (panelWidth - (totalBands-1)*gap) % totalBands
	if b < extra {
		return base + 1
	}
	return base
}

// interpolateBandColumns builds per-column levels by interpolating between neighboring bands.
func interpolateBandColumns(bands []float64, bandCols []int) []float64 {
	totalCols := 0
	for _, width := range bandCols {
		totalCols += width
	}

	cols := make([]float64, totalCols)
	offset := 0
	for b, level := range bands {
		width := bandCols[b]
		if width <= 0 {
			continue
		}
		nextLevel := level
		if b+1 < len(bands) {
			nextLevel = bands[b+1]
		}
		for c := range width {
			t := float64(c) / float64(width)
			cols[offset+c] = level*(1-t) + nextLevel*t
		}
		offset += width
	}
	return cols
}

func sampleBandLinear(bands []float64, pos float64) float64 {
	switch len(bands) {
	case 0:
		return 0
	case 1:
		return bands[0]
	}
	if pos <= 0 {
		return bands[0]
	}
	last := float64(len(bands) - 1)
	if pos >= last {
		return bands[len(bands)-1]
	}
	idx := int(pos)
	frac := pos - float64(idx)
	return bands[idx]*(1-frac) + bands[idx+1]*frac
}

func resampleBandsLinear(bands []float64, totalCols int) []float64 {
	if totalCols <= 0 || len(bands) == 0 {
		return nil
	}
	if len(bands) == totalCols {
		out := make([]float64, len(bands))
		copy(out, bands)
		return out
	}
	out := make([]float64, totalCols)
	if totalCols == 1 {
		out[0] = sampleBandLinear(bands, float64(len(bands)-1)/2)
		return out
	}
	last := float64(len(bands) - 1)
	for col := range totalCols {
		pos := float64(col) / float64(totalCols-1) * last
		out[col] = sampleBandLinear(bands, pos)
	}
	return out
}

func averageSpectrumRangeLinear(magnitudes []float64, loPos, hiPos float64) float64 {
	if len(magnitudes) == 0 {
		return 0
	}
	minPos := 1.0
	maxPos := float64(len(magnitudes) - 1)
	loPos = max(minPos, min(maxPos, loPos))
	hiPos = max(loPos, min(maxPos, hiPos))
	span := hiPos - loPos
	if span <= 0 {
		return sampleBandLinear(magnitudes, loPos)
	}
	sampleCount := max(4, min(32, int(math.Ceil(span*2))))
	var sum float64
	for i := range sampleCount {
		t := (float64(i) + 0.5) / float64(sampleCount)
		sum += sampleBandLinear(magnitudes, loPos+t*span)
	}
	return sum / float64(sampleCount)
}

// Pre-built styles for spectrum bar colors to avoid per-frame allocation.
var (
	specLowStyle  = lipgloss.NewStyle().Foreground(spectrumLow)
	specMidStyle  = lipgloss.NewStyle().Foreground(spectrumMid)
	specHighStyle = lipgloss.NewStyle().Foreground(spectrumHigh)
)

type visTickContext struct {
	Now           time.Time
	Playing       bool
	Paused        bool
	OverlayActive bool
	Analyze       func(visAnalysisSpec) []float64
}

type visAnalysisSpec struct {
	BandCount int
	FFTSize   int
}

func spectrumAnalysisSpec(bandCount int) visAnalysisSpec {
	return visAnalysisSpec{
		BandCount: bandCount,
		FFTSize:   defaultFFTSize,
	}
}

func normalizeAnalysisSpec(spec visAnalysisSpec) visAnalysisSpec {
	if spec.BandCount < 0 {
		spec.BandCount = 0
	}
	if spec.FFTSize <= 0 {
		spec.FFTSize = defaultFFTSize
	}
	return spec
}

type visModeDriver interface {
	AnalysisSpec(*Visualizer) visAnalysisSpec
	Render(*Visualizer) string
	Tick(*Visualizer, visTickContext)
	TickInterval(*Visualizer, visTickContext) time.Duration
	OnEnter(*Visualizer)
	OnLeave(*Visualizer)
}

// visEntry pairs a display name with a factory for that mode's visModeDriver.
type visEntry struct {
	name      string
	newDriver func() visModeDriver
}

type renderOnlyDriver struct {
	spec   visAnalysisSpec
	render func(*Visualizer, []float64) string
}

func (d *renderOnlyDriver) AnalysisSpec(*Visualizer) visAnalysisSpec {
	return d.spec
}

func (d *renderOnlyDriver) Render(v *Visualizer) string {
	return d.render(v, v.bands)
}

func (d *renderOnlyDriver) Tick(v *Visualizer, ctx visTickContext) {
	defaultDriverTick(v, ctx, d.spec)
}

func (*renderOnlyDriver) TickInterval(_ *Visualizer, ctx visTickContext) time.Duration {
	return defaultDriverTickInterval(ctx)
}

func (*renderOnlyDriver) OnEnter(*Visualizer) {}

func (*renderOnlyDriver) OnLeave(*Visualizer) {}

type noOpDriver struct{}

func (*noOpDriver) AnalysisSpec(*Visualizer) visAnalysisSpec { return visAnalysisSpec{} }

func (*noOpDriver) Render(*Visualizer) string { return "" }

func (*noOpDriver) Tick(*Visualizer, visTickContext) {}

func (*noOpDriver) TickInterval(*Visualizer, visTickContext) time.Duration { return tickSlow }

func (*noOpDriver) OnEnter(*Visualizer) {}

func (*noOpDriver) OnLeave(*Visualizer) {}

func newRenderOnlyDriver(spec visAnalysisSpec, render func(*Visualizer, []float64) string) func() visModeDriver {
	return func() visModeDriver {
		return &renderOnlyDriver{spec: normalizeAnalysisSpec(spec), render: render}
	}
}

func newNoOpDriver() visModeDriver {
	return &noOpDriver{}
}

func defaultDriverTick(v *Visualizer, ctx visTickContext, spec visAnalysisSpec) {
	if ctx.OverlayActive || ctx.Analyze == nil {
		return
	}
	spec = normalizeAnalysisSpec(spec)
	bands := ctx.Analyze(spec)
	if spec.BandCount > 0 {
		v.bands = bands
	}
}

// defaultDriverTickInterval uses fast ticks only when audio is actively playing with a live
// visualizer. Paused/stopped playback has no new audio samples, so slow ticks are sufficient
// and save CPU/GPU repaints. Overlays use slow ticks as well.
func defaultDriverTickInterval(ctx visTickContext) time.Duration {
	if ctx.OverlayActive {
		return tickSlow
	}
	if ctx.Playing {
		return tickFast
	}
	return tickSlow
}

// Visualizer performs FFT analysis and renders spectrum bars.
type Visualizer struct {
	prevBySpec     map[visAnalysisSpec][]float64
	edgeCache      map[int][]float64
	fftBufCache    map[int][]float64
	windowCache    map[int][]float64
	bands          []float64
	sr             float64
	Mode           VisMode
	Rows           int       // display height in terminal rows (default 5)
	waveBuf        []float64 // raw samples for wave mode
	frame          uint64    // tick-driven animation clock
	sampleBuf      []float64 // reusable buffer for reading audio tap samples
	drivers        [visCount]visModeDriver
	activeMode     VisMode
	activeModeSet  bool
	refreshPending bool
	luaVisNames    []string
	luaRender      luaVisRenderer
	luaDriverCache map[int]visModeDriver
}

// luaVisRenderer is the callback type for rendering a Lua visualizer frame.
type luaVisRenderer func(name string, bands [defaultSpectrumBands]float64, rows, cols int, frame uint64) string

// NewVisualizer creates a Visualizer for the given sample rate.
func NewVisualizer(sampleRate float64) *Visualizer {
	return &Visualizer{
		sr:             sampleRate,
		sampleBuf:      make([]float64, defaultFFTSize),
		Rows:           defaultVisRows,
		bands:          make([]float64, defaultSpectrumBands),
		prevBySpec:     make(map[visAnalysisSpec][]float64),
		edgeCache:      make(map[int][]float64),
		fftBufCache:    make(map[int][]float64),
		windowCache:    make(map[int][]float64),
		luaDriverCache: make(map[int]visModeDriver),
		refreshPending: true,
	}
}

// CycleMode advances to the next visualizer mode, including Lua visualizers.
func (v *Visualizer) CycleMode() {
	total := visCount + VisMode(len(v.luaVisNames))
	v.Mode = (v.Mode + 1) % total
}

// visModes is the single source of truth for all visualizer modes.
// To add a new mode: add a const, add one line here, create a vis_*.go file.
var visModes = [visCount]visEntry{
	VisBars:        {"Bars", newRenderOnlyDriver(spectrumAnalysisSpec(defaultSpectrumBands), (*Visualizer).renderBars)},
	VisBarsDot:     {"BarsDot", newRenderOnlyDriver(spectrumAnalysisSpec(defaultSpectrumBands), (*Visualizer).renderBarsDot)},
	VisRain:        {"Rain", newRenderOnlyDriver(spectrumAnalysisSpec(defaultSpectrumBands), (*Visualizer).renderRain)},
	VisBarsOutline: {"BarsOutline", newRenderOnlyDriver(spectrumAnalysisSpec(defaultSpectrumBands), (*Visualizer).renderBarsOutline)},
	VisBricks:      {"Bricks", newRenderOnlyDriver(spectrumAnalysisSpec(defaultSpectrumBands), (*Visualizer).renderBricks)},
	VisColumns:     {"Columns", newRenderOnlyDriver(spectrumAnalysisSpec(defaultSpectrumBands), (*Visualizer).renderColumns)},
	VisClassicPeak: {"ClassicPeak", newClassicPeakDriver},
	VisWave:        {"Wave", newRenderOnlyDriver(spectrumAnalysisSpec(0), func(v *Visualizer, _ []float64) string { return v.renderWave() })},
	VisScatter:     {"Scatter", newRenderOnlyDriver(spectrumAnalysisSpec(defaultSpectrumBands), (*Visualizer).renderScatter)},
	VisFlame:       {"Flame", newRenderOnlyDriver(spectrumAnalysisSpec(defaultSpectrumBands), (*Visualizer).renderFlame)},
	VisRetro:       {"Retro", newRenderOnlyDriver(spectrumAnalysisSpec(defaultSpectrumBands), (*Visualizer).renderRetro)},
	VisPulse:       {"Pulse", newRenderOnlyDriver(spectrumAnalysisSpec(defaultSpectrumBands), (*Visualizer).renderPulse)},
	VisMatrix:      {"Matrix", newRenderOnlyDriver(spectrumAnalysisSpec(defaultSpectrumBands), (*Visualizer).renderMatrix)},
	VisBinary:      {"Binary", newRenderOnlyDriver(spectrumAnalysisSpec(defaultSpectrumBands), (*Visualizer).renderBinary)},
	VisSakura:      {"Sakura", newRenderOnlyDriver(spectrumAnalysisSpec(defaultSpectrumBands), (*Visualizer).renderSakura)},
	VisFirework:    {"Firework", newRenderOnlyDriver(spectrumAnalysisSpec(defaultSpectrumBands), (*Visualizer).renderFirework)},
	VisLogo:        {"Logo", newRenderOnlyDriver(spectrumAnalysisSpec(defaultSpectrumBands), (*Visualizer).renderLogo)},
	VisTerrain:     {"Terrain", newTerrainDriver},
	VisGlitch:      {"Glitch", newRenderOnlyDriver(spectrumAnalysisSpec(defaultSpectrumBands), (*Visualizer).renderGlitch)},
	VisScope:       {"Scope", newRenderOnlyDriver(spectrumAnalysisSpec(0), func(v *Visualizer, _ []float64) string { return v.renderScope() })},
	VisHeartbeat:   {"Heartbeat", newRenderOnlyDriver(spectrumAnalysisSpec(0), func(v *Visualizer, _ []float64) string { return v.renderHeartbeat() })},
	VisButterfly:   {"Butterfly", newRenderOnlyDriver(spectrumAnalysisSpec(defaultSpectrumBands), (*Visualizer).renderButterfly)},
	VisLightning:   {"Lightning", newRenderOnlyDriver(spectrumAnalysisSpec(defaultSpectrumBands), (*Visualizer).renderLightning)},
	VisNone:        {"None", newNoOpDriver},
}

var visNameMap map[string]VisMode

func init() {
	visNameMap = make(map[string]VisMode, visCount)
	for i := range visCount {
		visNameMap[strings.ToLower(visModes[i].name)] = VisMode(i)
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

func buildSpectrumEdges(count int) []float64 {
	if count <= 0 {
		return nil
	}
	edges := make([]float64, count+1)
	lastAnchor := len(legacySpectrumEdges) - 1
	for i := range count + 1 {
		numerator := i * lastAnchor
		idx := numerator / count
		if idx >= lastAnchor {
			edges[i] = legacySpectrumEdges[lastAnchor]
			continue
		}
		if numerator%count == 0 {
			edges[i] = legacySpectrumEdges[idx]
			continue
		}
		frac := float64(numerator%count) / float64(count)
		lo := legacySpectrumEdges[idx]
		hi := legacySpectrumEdges[idx+1]
		edges[i] = math.Pow(10, math.Log10(lo)*(1-frac)+math.Log10(hi)*frac)
	}
	return edges
}

func buildHannWindow(size int) []float64 {
	window := make([]float64, size)
	for i := range size {
		window[i] = 0.5 * (1 - math.Cos(2*math.Pi*float64(i)/float64(size-1)))
	}
	return window
}

func (v *Visualizer) prevBands(spec visAnalysisSpec) []float64 {
	if prev, ok := v.prevBySpec[spec]; ok {
		return prev
	}
	prev := make([]float64, spec.BandCount)
	v.prevBySpec[spec] = prev
	return prev
}

func (v *Visualizer) spectrumEdges(count int) []float64 {
	if edges, ok := v.edgeCache[count]; ok {
		return edges
	}
	edges := buildSpectrumEdges(count)
	v.edgeCache[count] = edges
	return edges
}

func (v *Visualizer) fftBuffer(size int) []float64 {
	if buf, ok := v.fftBufCache[size]; ok {
		return buf
	}
	buf := make([]float64, size)
	v.fftBufCache[size] = buf
	return buf
}

func (v *Visualizer) hannWindow(size int) []float64 {
	if window, ok := v.windowCache[size]; ok {
		return window
	}
	window := buildHannWindow(size)
	v.windowCache[size] = window
	return window
}

func (v *Visualizer) resetSpectrumHistory() {
	if v == nil {
		return
	}
	clear(v.prevBySpec)
}

func (v *Visualizer) ensureSampleBuf(size int) []float64 {
	size = normalizeAnalysisSpec(visAnalysisSpec{FFTSize: size}).FFTSize
	if cap(v.sampleBuf) < size {
		v.sampleBuf = make([]float64, size)
	} else {
		v.sampleBuf = v.sampleBuf[:size]
	}
	return v.sampleBuf
}

// RegisterLuaVisualizers adds Lua visualizer names so they can be cycled
// through with the v key. renderer is called when a Lua visualizer is active.
func (v *Visualizer) RegisterLuaVisualizers(names []string, renderer luaVisRenderer) {
	v.luaVisNames = names
	v.luaRender = renderer
	clear(v.luaDriverCache)
	// Add to name map for StringToVisMode lookups.
	for i, name := range names {
		visNameMap[strings.ToLower(name)] = visCount + VisMode(i)
	}
}

// Analyze runs FFT on raw audio samples and returns normalized band levels (0-1).
func (v *Visualizer) Analyze(samples []float64, spec visAnalysisSpec) []float64 {
	spec = normalizeAnalysisSpec(spec)

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

	if spec.BandCount <= 0 {
		return nil
	}

	prev := v.prevBands(spec)
	bands := make([]float64, spec.BandCount)
	if len(samples) == 0 {
		// Decay previous values when no audio data
		for b := range spec.BandCount {
			bands[b] = prev[b] * 0.8
			prev[b] = bands[b]
		}
		return bands
	}

	// Zero-fill and copy into reusable buffer
	buf := v.fftBuffer(spec.FFTSize)
	clear(buf)
	copy(buf, samples)

	// Apply the cached Hann window to reduce spectral leakage.
	window := v.hannWindow(spec.FFTSize)
	for i := range spec.FFTSize {
		buf[i] *= window[i]
	}

	// Compute FFT
	spectrum := fft.FFTReal(buf)
	halfLen := len(spectrum) / 2
	magnitudes := buf[:halfLen]
	magnitudes[0] = 0
	for i := 1; i < halfLen; i++ {
		magnitudes[i] = cmplx.Abs(spectrum[i])
	}

	binHz := v.sr / float64(spec.FFTSize)
	edges := v.spectrumEdges(spec.BandCount)

	// Average the FFT envelope across each band span, including fractional-bin ranges.
	for b := range spec.BandCount {
		sum := averageSpectrumRangeLinear(magnitudes, edges[b]/binHz, edges[b+1]/binHz)

		// Convert to dB-like scale and normalize to 0-1
		if sum > 0 {
			bands[b] = (20*math.Log10(sum) + 10) / 50
		}
		bands[b] = max(0, min(1, bands[b]))

		// Temporal smoothing: fast attack, slow decay
		if bands[b] > prev[b] {
			bands[b] = bands[b]*0.6 + prev[b]*0.4
		} else {
			bands[b] = bands[b]*0.25 + prev[b]*0.75
		}
		prev[b] = bands[b]
	}

	return bands
}

// Render dispatches to the active visualizer mode.
func (v *Visualizer) Render() string {
	driver := v.syncDriverMode()
	if driver == nil {
		return ""
	}
	return driver.Render(v)
}

func (v *Visualizer) requestRefresh() {
	if v != nil {
		v.refreshPending = true
	}
}

func (v *Visualizer) consumeRefresh() bool {
	if v == nil || !v.refreshPending {
		return false
	}
	v.refreshPending = false
	return true
}

func (v *Visualizer) TickInterval(ctx visTickContext) time.Duration {
	driver := v.syncDriverMode()
	if driver == nil {
		return tickSlow
	}
	return driver.TickInterval(v, ctx)
}

func (v *Visualizer) Tick(ctx visTickContext) {
	driver := v.syncDriverMode()
	if driver == nil {
		return
	}
	v.refreshPending = false
	if v.Mode != VisNone && !ctx.OverlayActive {
		v.frame++
	}
	driver.Tick(v, ctx)
}

func (v *Visualizer) driverFor(mode VisMode) visModeDriver {
	if v == nil || mode < 0 {
		return nil
	}
	if mode >= visCount {
		idx := int(mode - visCount)
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

type luaModeDriver struct {
	index int
}

func (*luaModeDriver) AnalysisSpec(*Visualizer) visAnalysisSpec {
	return spectrumAnalysisSpec(defaultSpectrumBands)
}

func (d *luaModeDriver) Render(v *Visualizer) string {
	if v == nil || d.index < 0 || d.index >= len(v.luaVisNames) || v.luaRender == nil {
		return ""
	}
	return v.luaRender(v.luaVisNames[d.index], luaBands(v.bands), v.Rows, panelWidth, v.frame)
}

func (d *luaModeDriver) Tick(v *Visualizer, ctx visTickContext) {
	defaultDriverTick(v, ctx, d.AnalysisSpec(v))
}

func (*luaModeDriver) TickInterval(_ *Visualizer, ctx visTickContext) time.Duration {
	return defaultDriverTickInterval(ctx)
}

func (*luaModeDriver) OnEnter(*Visualizer) {}

func (*luaModeDriver) OnLeave(*Visualizer) {}

func luaBands(src []float64) [defaultSpectrumBands]float64 {
	var bands [defaultSpectrumBands]float64
	copy(bands[:], src)
	return bands
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
		prevSpec := visAnalysisSpec{}
		if prev != nil {
			prevSpec = normalizeAnalysisSpec(prev.AnalysisSpec(v))
		}
		nextSpec := visAnalysisSpec{}
		if driver != nil {
			nextSpec = normalizeAnalysisSpec(driver.AnalysisSpec(v))
		}
		if (prevSpec.BandCount == 0) != (nextSpec.BandCount == 0) {
			v.resetSpectrumHistory()
		}
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
