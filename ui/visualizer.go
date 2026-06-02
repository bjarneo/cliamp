package ui

import (
	"math"
	"strings"
	"time"

	"charm.land/lipgloss/v2"
)

const (
	DefaultSpectrumBands = 10
	defaultFFTSize       = 2048
	DefaultVisRows       = 5
	minSpectrumHz        = 20.0
	maxSpectrumHz        = 20000.0
	// Cap on dt fed into smoothing easing — long gaps (sleep, paused, stalled
	// frame) step like ~1 frame instead of integrating over a huge interval.
	maxSmoothDtFrames = 10
)

var legacySpectrumEdges = [DefaultSpectrumBands + 1]float64{
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
	VisBubbles                    // rising hollow ring bubbles
	VisLogo                       // CLIAMP pixel text
	VisTerrain                    // scrolling side-view mountain range
	VisScope                      // Lissajous XY oscilloscope
	VisHeartbeat                  // ECG pulse monitor trace
	VisButterfly                  // mirrored Rorschach spectrum
	VisAscii                      // dense shade-block columns (website style)
	VisFirefly                    // firefly meadow at dusk
	VisMosaic                     // static heatmap of flickering tiles
	VisSand                       // falling-sand cellular automaton
	VisGeyser                     // bass-driven particle fountain
	VisClassicLED                 // Winamp 2.9 LED matrix with falling peak caps
	VisNone                       // hidden — no visualizer
	VisCount                      // sentinel for cycling
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
// 1-char gaps exactly fill PanelWidth. The remainder is distributed across the
// first few bands.
func visBandWidth(totalBands, b int) int {
	const gap = 1
	if totalBands <= 0 {
		return 0
	}
	base := (PanelWidth - (totalBands-1)*gap) / totalBands
	extra := (PanelWidth - (totalBands-1)*gap) % totalBands
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
	specLowStyle  = lipgloss.NewStyle().Foreground(SpectrumLow)
	specMidStyle  = lipgloss.NewStyle().Foreground(SpectrumMid)
	specHighStyle = lipgloss.NewStyle().Foreground(SpectrumHigh)
)

// Raw ANSI wrappers for the spectrum styles. Caching these once lets every
// style-run flush skip lipgloss.Render (which allocates a fresh wrapped string
// per call) and instead stream prefix + body + suffix into an existing builder.
// Rebuilt via refreshSpecANSI on theme changes.
var (
	specLowPrefix, specLowSuffix   string
	specMidPrefix, specMidSuffix   string
	specHighPrefix, specHighSuffix string
)

func init() {
	refreshSpecANSI()
}

func refreshSpecANSI() {
	specLowPrefix, specLowSuffix = splitStyleAroundProbe(specLowStyle)
	specMidPrefix, specMidSuffix = splitStyleAroundProbe(specMidStyle)
	specHighPrefix, specHighSuffix = splitStyleAroundProbe(specHighStyle)
}

// splitStyleAroundProbe renders a rare marker through the style and splits the
// output around it, yielding the ANSI prefix and suffix the style applies.
// Works for plain Foreground-only styles; adding borders or padding would
// invalidate the split.
func splitStyleAroundProbe(s lipgloss.Style) (prefix, suffix string) {
	const probe = "\uFFFC"
	rendered := s.Render(probe)
	idx := strings.Index(rendered, probe)
	if idx < 0 {
		return "", ""
	}
	return rendered[:idx], rendered[idx+len(probe):]
}

type VisTickContext struct {
	Now           time.Time
	Playing       bool
	Paused        bool
	OverlayActive bool
	Analyze       func(VisAnalysisSpec) []float64
}

type VisAnalysisSpec struct {
	BandCount int
	FFTSize   int
}

func spectrumAnalysisSpec(bandCount int) VisAnalysisSpec {
	return VisAnalysisSpec{
		BandCount: bandCount,
		FFTSize:   defaultFFTSize,
	}
}

func NormalizeAnalysisSpec(spec VisAnalysisSpec) VisAnalysisSpec {
	if spec.BandCount < 0 {
		spec.BandCount = 0
	}
	if spec.FFTSize <= 0 {
		spec.FFTSize = defaultFFTSize
	}
	return spec
}

type visModeDriver interface {
	AnalysisSpec(*Visualizer) VisAnalysisSpec
	Render(*Visualizer) string
	Tick(*Visualizer, VisTickContext)
	TickInterval(*Visualizer, VisTickContext) time.Duration
	OnEnter(*Visualizer)
	OnLeave(*Visualizer)
}

// visEntry pairs a display name with a factory for that mode's visModeDriver.
type visEntry struct {
	name      string
	newDriver func() visModeDriver
}

type renderOnlyDriver struct {
	spec         VisAnalysisSpec
	render       func(*Visualizer, []float64) string
	tickDuration time.Duration // 0 = use defaultDriverTickInterval
}

func (d *renderOnlyDriver) AnalysisSpec(*Visualizer) VisAnalysisSpec {
	return d.spec
}

func (d *renderOnlyDriver) Render(v *Visualizer) string {
	return d.render(v, v.SmoothedBands())
}

func (d *renderOnlyDriver) Tick(v *Visualizer, ctx VisTickContext) {
	defaultDriverTick(v, ctx, d.spec)
}

func (d *renderOnlyDriver) TickInterval(_ *Visualizer, ctx VisTickContext) time.Duration {
	if d.tickDuration > 0 && ctx.Playing && !ctx.OverlayActive {
		return d.tickDuration
	}
	return defaultDriverTickInterval(ctx)
}

func (*renderOnlyDriver) OnEnter(*Visualizer) {}

func (*renderOnlyDriver) OnLeave(*Visualizer) {}

type noOpDriver struct{}

func (*noOpDriver) AnalysisSpec(*Visualizer) VisAnalysisSpec { return VisAnalysisSpec{} }

func (*noOpDriver) Render(*Visualizer) string { return "" }

func (*noOpDriver) Tick(*Visualizer, VisTickContext) {}

func (*noOpDriver) TickInterval(*Visualizer, VisTickContext) time.Duration { return TickSlow }

func (*noOpDriver) OnEnter(*Visualizer) {}

func (*noOpDriver) OnLeave(*Visualizer) {}

func newRenderOnlyDriver(spec VisAnalysisSpec, render func(*Visualizer, []float64) string) func() visModeDriver {
	return func() visModeDriver {
		return &renderOnlyDriver{spec: NormalizeAnalysisSpec(spec), render: render}
	}
}

func newFastRenderOnlyDriver(spec VisAnalysisSpec, tick time.Duration, render func(*Visualizer, []float64) string) func() visModeDriver {
	return func() visModeDriver {
		return &renderOnlyDriver{spec: NormalizeAnalysisSpec(spec), render: render, tickDuration: tick}
	}
}

func newNoOpDriver() visModeDriver {
	return &noOpDriver{}
}

func defaultDriverTick(v *Visualizer, ctx VisTickContext, spec VisAnalysisSpec) {
	if ctx.OverlayActive {
		// Reset both clocks so the first tick after dismissal analyzes
		// immediately and smoothing dt resets to a single-frame step.
		v.lastAnalyzeAt = time.Time{}
		v.lastSmoothTick = time.Time{}
		return
	}
	spec = NormalizeAnalysisSpec(spec)
	if ctx.Analyze != nil {
		// Decouple FFT cadence from animation cadence: skip Analyze if we ran
		// it recently. Animation still advances every tick via advanceSmoothing.
		due := v.lastAnalyzeAt.IsZero() || ctx.Now.IsZero() ||
			ctx.Now.Sub(v.lastAnalyzeAt) >= TickAnalyze
		if due {
			bands := ctx.Analyze(spec)
			if spec.BandCount > 0 {
				v.bands = bands
			}
			if !ctx.Now.IsZero() {
				v.lastAnalyzeAt = ctx.Now
			}
		}
	}
	// Always ease toward the most recent target — even when Analyze is nil
	// or skipped — so animation stays smooth across analysis gaps.
	if spec.BandCount > 0 {
		v.advanceSmoothing(ctx.Now)
	}
}

// defaultDriverTickInterval uses fast ticks only when audio is actively playing
// with a live visualizer. Paused/stopped playback has no new audio samples, so
// slow ticks are sufficient and save CPU/GPU repaints. Overlays use slow ticks
// as well. Bar-style spectrum drivers opt into TickAnim via newFastRenderOnlyDriver.
func defaultDriverTickInterval(ctx VisTickContext) time.Duration {
	if ctx.OverlayActive {
		return TickSlow
	}
	if ctx.Playing {
		return TickFast
	}
	return TickSlow
}

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
	Rows            int       // display height in terminal rows (default 5)
	waveBuf         []float64 // raw samples for wave mode
	waveYBuf        []int     // reusable y-position buffer for wave rendering
	frame           uint64    // tick-driven animation clock
	sampleBuf       []float64 // reusable buffer for reading audio tap samples
	drivers         [VisCount]visModeDriver
	activeMode      VisMode
	activeModeSet   bool
	refreshPending  bool
	luaVisNames     []string
	luaRender       LuaVisRenderer
	luaDriverCache  map[int]visModeDriver
	pulseCoordCache *pulseCoords
}

// LuaVisRenderer is the callback type for rendering a Lua visualizer frame.
type LuaVisRenderer func(name string, bands [DefaultSpectrumBands]float64, rows, cols int, frame uint64) string

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

// CycleMode advances to the next visualizer mode, including Lua visualizers.
func (v *Visualizer) CycleMode() {
	total := VisCount + VisMode(len(v.luaVisNames))
	v.Mode = (v.Mode + 1) % total
}

// visModes is the single source of truth for all visualizer modes.
// To add a new mode: add a const, add one line here, create a vis_*.go file.
var visModes = [VisCount]visEntry{
	VisBars:        {"Bars", newFastRenderOnlyDriver(spectrumAnalysisSpec(DefaultSpectrumBands), TickAnim, (*Visualizer).renderBars)},
	VisBarsDot:     {"BarsDot", newFastRenderOnlyDriver(spectrumAnalysisSpec(DefaultSpectrumBands), TickAnim, (*Visualizer).renderBarsDot)},
	VisRain:        {"Rain", newRenderOnlyDriver(spectrumAnalysisSpec(DefaultSpectrumBands), (*Visualizer).renderRain)},
	VisBarsOutline: {"BarsOutline", newFastRenderOnlyDriver(spectrumAnalysisSpec(DefaultSpectrumBands), TickAnim, (*Visualizer).renderBarsOutline)},
	VisBricks:      {"Bricks", newFastRenderOnlyDriver(spectrumAnalysisSpec(DefaultSpectrumBands), TickAnim, (*Visualizer).renderBricks)},
	VisColumns:     {"Columns", newFastRenderOnlyDriver(spectrumAnalysisSpec(DefaultSpectrumBands), TickAnim, (*Visualizer).renderColumns)},
	VisClassicPeak: {"ClassicPeak", newClassicPeakDriver},
	VisWave:        {"Wave", newFastRenderOnlyDriver(spectrumAnalysisSpec(0), TickWave, func(v *Visualizer, _ []float64) string { return v.renderWave() })},
	VisScatter:     {"Scatter", newRenderOnlyDriver(spectrumAnalysisSpec(DefaultSpectrumBands), (*Visualizer).renderScatter)},
	VisFlame:       {"Flame", newFlameDriver},
	VisRetro:       {"Retro", newRenderOnlyDriver(spectrumAnalysisSpec(DefaultSpectrumBands), (*Visualizer).renderRetro)},
	VisPulse:       {"Pulse", newRenderOnlyDriver(spectrumAnalysisSpec(DefaultSpectrumBands), (*Visualizer).renderPulse)},
	VisMatrix:      {"Matrix", newRenderOnlyDriver(spectrumAnalysisSpec(DefaultSpectrumBands), (*Visualizer).renderMatrix)},
	VisBinary:      {"Binary", newRenderOnlyDriver(spectrumAnalysisSpec(DefaultSpectrumBands), (*Visualizer).renderBinary)},
	VisSakura:      {"Sakura", newRenderOnlyDriver(spectrumAnalysisSpec(DefaultSpectrumBands), (*Visualizer).renderSakura)},
	VisFirework:    {"Firework", newRenderOnlyDriver(spectrumAnalysisSpec(DefaultSpectrumBands), (*Visualizer).renderFirework)},
	VisBubbles:     {"Bubbles", newRenderOnlyDriver(spectrumAnalysisSpec(DefaultSpectrumBands), (*Visualizer).renderBubbles)},
	VisLogo:        {"Logo", newRenderOnlyDriver(spectrumAnalysisSpec(DefaultSpectrumBands), (*Visualizer).renderLogo)},
	VisTerrain:     {"Terrain", newTerrainDriver},
	VisScope:       {"Scope", newFastRenderOnlyDriver(spectrumAnalysisSpec(0), TickWave, func(v *Visualizer, _ []float64) string { return v.renderScope() })},
	VisHeartbeat:   {"Heartbeat", newFastRenderOnlyDriver(spectrumAnalysisSpec(0), TickWave, func(v *Visualizer, _ []float64) string { return v.renderHeartbeat() })},
	VisButterfly:   {"Butterfly", newRenderOnlyDriver(spectrumAnalysisSpec(DefaultSpectrumBands), (*Visualizer).renderButterfly)},
	VisAscii:       {"Ascii", newFastRenderOnlyDriver(spectrumAnalysisSpec(DefaultSpectrumBands), TickAnim, (*Visualizer).renderAscii)},
	VisFirefly:     {"Firefly", newRenderOnlyDriver(spectrumAnalysisSpec(DefaultSpectrumBands), (*Visualizer).renderFirefly)},
	VisMosaic:      {"Mosaic", newMosaicDriver},
	VisSand:        {"Sand", newSandDriver},
	VisGeyser:      {"Geyser", newGeyserDriver},
	VisClassicLED:  {"ClassicLED", newClassicLEDDriver},
	VisNone:        {"None", newNoOpDriver},
}

var visNameMap map[string]VisMode

func init() {
	visNameMap = make(map[string]VisMode, VisCount)
	for i := range VisCount {
		visNameMap[strings.ToLower(visModes[i].name)] = VisMode(i)
	}
}

// ModeName returns the display name of the current mode.
func (v *Visualizer) ModeName() string {
	if v.Mode < VisCount {
		return visModes[v.Mode].name
	}
	luaIdx := int(v.Mode - VisCount)
	if luaIdx < len(v.luaVisNames) {
		return v.luaVisNames[luaIdx]
	}
	return "Unknown"
}

// StringToVisModeExact converts a name to VisMode, returning false if not found.
func StringToVisModeExact(name string) (VisMode, bool) {
	mode, ok := visNameMap[strings.ToLower(name)]
	return mode, ok
}

// VisModeNames returns the display names of all built-in visualizer modes.
func VisModeNames() []string {
	names := make([]string, VisCount)
	for i := range VisCount {
		names[i] = visModes[i].name
	}
	return names
}

// AllModeNames returns the display names of every selectable visualizer mode in
// cycle order: built-in modes followed by any registered Lua visualizers. The
// index of each name equals its VisMode value, so a picker can map a list row
// directly to a mode.
func (v *Visualizer) AllModeNames() []string {
	return append(VisModeNames(), v.luaVisNames...)
}

// SetMode switches to mode if it is within range (built-in or Lua) and requests
// a refresh. Out-of-range values are ignored, matching the SetVisualizer guard.
func (v *Visualizer) SetMode(mode VisMode) {
	if mode < 0 || mode >= VisCount+VisMode(len(v.luaVisNames)) {
		return
	}
	v.Mode = mode
	v.RequestRefresh()
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

func (v *Visualizer) prevBands(spec VisAnalysisSpec) []float64 {
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

func (v *Visualizer) fftComplexBuffer(size int) []complex128 {
	if buf, ok := v.fftCplxCache[size]; ok {
		return buf
	}
	buf := make([]complex128, size)
	v.fftCplxCache[size] = buf
	return buf
}

func (v *Visualizer) fftTwiddles(size int) []complex128 {
	if w, ok := v.fftTwiddleCache[size]; ok {
		return w
	}
	w := buildTwiddles(size)
	v.fftTwiddleCache[size] = w
	return w
}

// resultBufFor returns a reusable []float64 for Analyze output, keyed by the
// full analysis spec so different specs with the same band count don't alias.
// Avoids allocating a new slice on every tick (20x/sec).
func (v *Visualizer) resultBufFor(spec VisAnalysisSpec) []float64 {
	if buf, ok := v.resultBufCache[spec]; ok {
		clear(buf)
		return buf
	}
	buf := make([]float64, spec.BandCount)
	v.resultBufCache[spec] = buf
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

func (v *Visualizer) EnsureSampleBuf(size int) []float64 {
	size = NormalizeAnalysisSpec(VisAnalysisSpec{FFTSize: size}).FFTSize
	if cap(v.sampleBuf) < size {
		v.sampleBuf = make([]float64, size)
	} else {
		v.sampleBuf = v.sampleBuf[:size]
	}
	return v.sampleBuf
}

// RegisterLuaVisualizers adds Lua visualizer names so they can be cycled
// through with the v key. renderer is called when a Lua visualizer is active.
func (v *Visualizer) RegisterLuaVisualizers(names []string, renderer LuaVisRenderer) {
	v.luaVisNames = names
	v.luaRender = renderer
	clear(v.luaDriverCache)
	// Add to name map for StringToVisModeExact lookups.
	for i, name := range names {
		visNameMap[strings.ToLower(name)] = VisCount + VisMode(i)
	}
}

// Analyze runs FFT on raw audio samples and returns normalized band levels (0-1).
func (v *Visualizer) Analyze(samples []float64, spec VisAnalysisSpec) []float64 {
	spec = NormalizeAnalysisSpec(spec)

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
	bands := v.resultBufFor(spec)

	// Silence gate: skip the FFT pipeline when input is empty or effectively
	// silent. A quick max-abs scan is two orders of magnitude cheaper than the
	// FFT and fires whenever playback is paused, between tracks, or quiet.
	silent := len(samples) == 0
	if !silent {
		maxAbs := 0.0
		for _, s := range samples {
			a := math.Abs(s)
			if a > maxAbs {
				maxAbs = a
			}
		}
		silent = maxAbs < 1e-5
	}
	if silent {
		for b := range spec.BandCount {
			bands[b] = prev[b] * 0.8
			prev[b] = bands[b]
		}
		return bands
	}

	// Window samples into the reusable complex FFT buffer. Any tail beyond the
	// provided samples stays zero from the previous run-through — we always
	// overwrite the first `have` entries and explicitly zero the rest below.
	cbuf := v.fftComplexBuffer(spec.FFTSize)
	window := v.hannWindow(spec.FFTSize)
	have := min(len(samples), spec.FFTSize)
	for i := range have {
		cbuf[i] = complex(samples[i]*window[i], 0)
	}
	for i := have; i < spec.FFTSize; i++ {
		cbuf[i] = 0
	}

	fftInPlace(cbuf, v.fftTwiddles(spec.FFTSize))

	// Power spectrum |X|^2 into the reusable float buffer. Skipping the sqrt
	// per bin halves the work compared to magnitudes; the log10 below absorbs
	// the factor of two so band values stay in the same [0,1] range.
	halfLen := spec.FFTSize / 2
	powers := v.fftBuffer(spec.FFTSize)[:halfLen]
	powers[0] = 0
	for i := 1; i < halfLen; i++ {
		re := real(cbuf[i])
		im := imag(cbuf[i])
		powers[i] = re*re + im*im
	}

	binHz := v.sr / float64(spec.FFTSize)
	edges := v.spectrumEdges(spec.BandCount)

	for b := range spec.BandCount {
		sum := averageSpectrumRangeLinear(powers, edges[b]/binHz, edges[b+1]/binHz)

		// Convert to dB-like scale. 10*log10(power) == 20*log10(magnitude).
		if sum > 0 {
			bands[b] = (10*math.Log10(sum) + 10) / 50
		}
		bands[b] = max(0, min(1, bands[b]))

		// Temporal smoothing: fast attack, slow decay.
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

// SampleBuf returns the internal sample buffer (for slicing after SamplesInto).
func (v *Visualizer) SampleBuf() []float64 { return v.sampleBuf }

// Bands returns the current spectrum band values.
func (v *Visualizer) Bands() []float64 { return v.bands }

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
	dt := TickAnim.Seconds()
	if !now.IsZero() && !v.lastSmoothTick.IsZero() {
		dt = now.Sub(v.lastSmoothTick).Seconds()
	}
	if dt <= 0 || dt > maxSmoothDtFrames*TickAnim.Seconds() {
		dt = TickAnim.Seconds()
	}
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

func (v *Visualizer) Tick(ctx VisTickContext) {
	driver := v.syncDriverMode()
	if driver == nil {
		return
	}
	v.refreshPending = false
	if ctx.Paused {
		return
	}
	if v.Mode != VisNone && !ctx.OverlayActive {
		v.frame++
	}
	driver.Tick(v, ctx)
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

type luaModeDriver struct {
	index int
}

func (*luaModeDriver) AnalysisSpec(*Visualizer) VisAnalysisSpec {
	return spectrumAnalysisSpec(DefaultSpectrumBands)
}

func (d *luaModeDriver) Render(v *Visualizer) string {
	if v == nil || d.index < 0 || d.index >= len(v.luaVisNames) || v.luaRender == nil {
		return ""
	}
	return v.luaRender(v.luaVisNames[d.index], luaBands(v.bands), v.Rows, PanelWidth, v.frame)
}

func (d *luaModeDriver) Tick(v *Visualizer, ctx VisTickContext) {
	defaultDriverTick(v, ctx, d.AnalysisSpec(v))
}

func (*luaModeDriver) TickInterval(_ *Visualizer, ctx VisTickContext) time.Duration {
	return defaultDriverTickInterval(ctx)
}

func (*luaModeDriver) OnEnter(*Visualizer) {}

func (*luaModeDriver) OnLeave(*Visualizer) {}

func luaBands(src []float64) [DefaultSpectrumBands]float64 {
	var bands [DefaultSpectrumBands]float64
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
// batching, using the same thresholds as specWrap.
func specTag(norm float64) int {
	if norm >= 0.6 {
		return 2
	}
	if norm >= 0.3 {
		return 1
	}
	return 0
}

// specWrap wraps body in the cached ANSI sequences for the spectrum color at
// the given row-bottom (0-1). One string concatenation instead of the several
// allocations a per-call lipgloss.Style.Render would perform.
func specWrap(rowBottom float64, body string) string {
	var prefix, suffix string
	switch specTag(rowBottom) {
	case 2:
		prefix, suffix = specHighPrefix, specHighSuffix
	case 1:
		prefix, suffix = specMidPrefix, specMidSuffix
	case 0:
		prefix, suffix = specLowPrefix, specLowSuffix
	}
	if prefix == "" {
		return body
	}
	return prefix + body + suffix
}

// flushStyleRun appends the accumulated run bytes to sb wrapped in the cached
// ANSI sequences for the given tag, then resets run. Tag -1 writes unstyled.
// Streaming via the pre-extracted prefix/suffix strings avoids allocating a
// fresh lipgloss.Render result on every flush (the hot path for Matrix/Pulse).
func flushStyleRun(sb *strings.Builder, run *strings.Builder, tag int) {
	if run.Len() == 0 {
		return
	}
	var prefix, suffix string
	switch tag {
	case 2:
		prefix, suffix = specHighPrefix, specHighSuffix
	case 1:
		prefix, suffix = specMidPrefix, specMidSuffix
	case 0:
		prefix, suffix = specLowPrefix, specLowSuffix
	}
	if prefix != "" {
		sb.WriteString(prefix)
	}
	// run.String() aliases the builder's backing array (no allocation) and we
	// copy those bytes into sb before run.Reset() releases the slice.
	sb.WriteString(run.String())
	if suffix != "" {
		sb.WriteString(suffix)
	}
	run.Reset()
}
