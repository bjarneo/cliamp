package ui

import (
	"math"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
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

func TestFastFrameDriverDoesNotCatchUpInactiveOrHiddenTime(t *testing.T) {
	tests := []struct {
		name    string
		suspend func(*Visualizer, time.Time)
	}{
		{
			name: "inactive",
			suspend: func(v *Visualizer, now time.Time) {
				v.Tick(VisTickContext{Now: now})
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

func TestInactiveBarsDecayToRestThenSuspend(t *testing.T) {
	withPanelWidth(t, 16)

	v := NewVisualizer(44100)
	activateMode(t, v, VisBars)
	v.bands = uniformBands(0.8)
	v.smoothedBands = append([]float64(nil), v.bands...)

	prev := append([]float64(nil), v.SmoothedBands()...)
	settled := false
	now := time.Unix(1, 0)
	for i := 0; i < 240; i++ {
		v.Tick(VisTickContext{Now: now.Add(time.Duration(i+1) * TickSlow)})
		cur := v.SmoothedBands()
		for b := range len(cur) {
			if cur[b] > prev[b]+1e-9 {
				t.Fatalf("inactive tick %d band %d rose %v -> %v, want monotonic decay", i, b, prev[b], cur[b])
			}
		}
		prev = append(prev[:0], cur...)
		if !v.DecayPending() {
			settled = true
			break
		}
	}
	if !settled {
		t.Fatal("inactive bars never settled to rest")
	}
	for i, got := range prev {
		if got >= decaySettledEpsilon {
			t.Fatalf("band %d = %v after decay, want below %v", i, got, decaySettledEpsilon)
		}
	}

	// Once settled, further inactive ticks suspend and leave the frame alone.
	frameBefore := v.Frame()
	v.Tick(VisTickContext{Now: now.Add(10 * time.Second)})
	if got := v.Frame(); got != frameBefore {
		t.Fatalf("settled inactive tick advanced frame %d -> %d", frameBefore, got)
	}
	for i, got := range v.SmoothedBands() {
		if math.Abs(got-prev[i]) > 1e-9 {
			t.Fatalf("settled inactive tick changed band %d %v -> %v", i, prev[i], got)
		}
	}
}

// sineSamples returns n samples of a loud 1 kHz tone at 44.1 kHz.
func sineSamples(n int) []float64 {
	s := make([]float64, n)
	for i := range s {
		s[i] = 0.8 * math.Sin(2*math.Pi*1000*float64(i)/44100)
	}
	return s
}

func TestInactiveWidthChangeDecaysReturnedSpecHistory(t *testing.T) {
	withPanelWidth(t, 30)
	v := NewVisualizer(44100)
	v.Rows = 5
	v.Mode = VisClassicLED
	sine := sineSamples(classicLEDFFTSize)
	loud := func(spec VisAnalysisSpec) []float64 { return v.Analyze(sine, spec) }
	now := time.Unix(1, 0)
	for _, width := range []int{30, 60} {
		PanelWidth = width
		for range 20 {
			now = now.Add(TickFast)
			v.Tick(VisTickContext{Now: now, Playing: true, Analyze: loud})
		}
	}

	// Stop and settle at width 60, then shrink back to 30 while stopped.
	for i := 0; i < 240 && v.DecayPending(); i++ {
		now = now.Add(TickFast)
		v.Tick(VisTickContext{Now: now})
	}
	PanelWidth = 30
	if !v.DecayPending() {
		t.Fatal("ClassicLED reported settled after a width change with its charged history")
	}
	for i := 0; i < 240 && v.DecayPending(); i++ {
		now = now.Add(TickFast)
		v.Tick(VisTickContext{Now: now})
	}
	if v.DecayPending() {
		t.Fatal("ClassicLED never settled after the width change")
	}

	silent := func(spec VisAnalysisSpec) []float64 { return v.Analyze(nil, spec) }
	v.Tick(VisTickContext{Now: now.Add(time.Second), Playing: true, Analyze: silent})
	if got := slices.Max(v.Bands()); got >= decaySettledEpsilon {
		t.Fatalf("resumed into silence with band level %v, want stale history decayed below %v", got, decaySettledEpsilon)
	}
}

func TestInactiveModeSwitchDecaysReturnedSpecHistory(t *testing.T) {
	withPanelWidth(t, 16)
	v := NewVisualizer(44100)
	v.Rows = 5
	v.Mode = VisClassicPeak
	sine := sineSamples(classicPeakFFTSize)
	loud := func(spec VisAnalysisSpec) []float64 { return v.Analyze(sine, spec) }
	now := time.Unix(1, 0)
	for range 20 {
		now = now.Add(TickFast)
		v.Tick(VisTickContext{Now: now, Playing: true, Analyze: loud})
	}

	// Stop, let Bars settle, then return to ClassicPeak while still stopped.
	v.Mode = VisBars
	for i := 0; i < 240 && v.DecayPending(); i++ {
		now = now.Add(TickFast)
		v.Tick(VisTickContext{Now: now})
	}
	v.Mode = VisClassicPeak
	if !v.DecayPending() {
		t.Fatal("ClassicPeak reported settled on re-entry with its charged history")
	}
	for i := 0; i < 240 && v.DecayPending(); i++ {
		now = now.Add(TickFast)
		v.Tick(VisTickContext{Now: now})
	}
	if v.DecayPending() {
		t.Fatal("ClassicPeak never settled after re-entry")
	}

	silent := func(spec VisAnalysisSpec) []float64 { return v.Analyze(nil, spec) }
	v.Tick(VisTickContext{Now: now.Add(time.Second), Playing: true, Analyze: silent})
	if got := slices.Max(v.Bands()); got >= decaySettledEpsilon {
		t.Fatalf("resumed into silence with band level %v, want stale history decayed below %v", got, decaySettledEpsilon)
	}
}

func TestSettledInactiveSpectrumReadsZero(t *testing.T) {
	withPanelWidth(t, 16)
	v := NewVisualizer(44100)
	v.Rows = 5
	v.Mode = VisBricks
	sine := sineSamples(defaultFFTSize)
	loud := func(spec VisAnalysisSpec) []float64 { return v.Analyze(sine, spec) }
	now := time.Unix(1, 0)
	for range 20 {
		now = now.Add(TickFast)
		v.Tick(VisTickContext{Now: now, Playing: true, Analyze: loud})
	}
	for i := 0; i < 240 && v.DecayPending(); i++ {
		now = now.Add(TickFast)
		v.Tick(VisTickContext{Now: now})
	}
	if v.DecayPending() {
		t.Fatal("bricks never settled while inactive")
	}
	if got := slices.Max(v.SmoothedBands()); got != 0 {
		t.Fatalf("settled smoothed band level = %v, want 0", got)
	}
	lines := strings.Split(v.Render(), "\n")
	if bottom := strings.TrimSpace(ansi.Strip(lines[len(lines)-1])); bottom != "" {
		t.Fatalf("settled bricks bottom row = %q, want blank", bottom)
	}
}

func TestInactiveRawSampleModeClearsWaveform(t *testing.T) {
	v := NewVisualizer(44100)
	activateMode(t, v, VisWave)
	v.waveBuf = []float64{-0.5, 0.5}
	ctx := VisTickContext{}

	if !v.DecayPending() {
		t.Fatal("raw waveform was considered settled before being cleared")
	}
	v.Tick(ctx)
	if len(v.waveBuf) != 0 {
		t.Fatalf("waveBuf len = %d after pause, want 0", len(v.waveBuf))
	}
	if v.DecayPending() {
		t.Fatal("raw waveform still settling after pause clear")
	}
}

func TestModeSwitchClearsSmoothedBands(t *testing.T) {
	v := NewVisualizer(44100)
	activateMode(t, v, VisBars)
	v.smoothedBands = uniformBands(0.8)

	activateMode(t, v, VisClassicPeak)
	if len(v.smoothedBands) != 0 {
		t.Fatalf("smoothedBands len = %d after mode switch, want 0", len(v.smoothedBands))
	}
}

func TestInactiveGeyserWaitsForParticles(t *testing.T) {
	withPanelWidth(t, 16)
	v := NewVisualizer(44100)
	v.Rows = 5
	activateMode(t, v, VisGeyser)
	driver := v.driverFor(VisGeyser).(*geyserDriver)
	driver.particles = []geyserParticle{{x: 1, y: float64(v.Rows * 4), tier: 1}}
	v.bands = uniformBands(0)
	v.smoothedBands = uniformBands(0)
	ctx := VisTickContext{}

	if !v.DecayPending() {
		t.Fatal("geyser was considered settled with a live particle")
	}
	v.Tick(ctx)
	if len(driver.particles) != 0 {
		t.Fatalf("geyser particles = %d after off-screen decay, want 0", len(driver.particles))
	}
	if v.DecayPending() {
		t.Fatal("geyser still settling after particles expired")
	}
}

func TestInactiveSandWaitsForExplosion(t *testing.T) {
	withPanelWidth(t, 16)
	v := NewVisualizer(44100)
	v.Rows = 5
	activateMode(t, v, VisSand)
	driver := v.driverFor(VisSand).(*sandDriver)
	driver.ensure(v.Rows*4, PanelWidth*2)
	driver.particles = []sandParticle{{x: 1, y: float64(v.Rows * 4), tier: 1}}
	driver.explosionTTL = 1
	v.bands = uniformBands(0)
	v.smoothedBands = uniformBands(0)
	ctx := VisTickContext{}

	if !v.DecayPending() {
		t.Fatal("sand was considered settled during an explosion")
	}
	v.Tick(ctx)
	if len(driver.particles) != 0 || driver.explosionTTL != 0 {
		t.Fatalf("sand explosion state after decay = particles %d, ttl %d", len(driver.particles), driver.explosionTTL)
	}
	if v.DecayPending() {
		t.Fatal("sand still settling after explosion expired")
	}
}

func TestInactiveSandWaitsForFallingGrains(t *testing.T) {
	withPanelWidth(t, 4)
	v := NewVisualizer(44100)
	v.Rows = 10
	activateMode(t, v, VisSand)
	driver := v.driverFor(VisSand).(*sandDriver)
	driver.ensure(v.Rows*4, PanelWidth*2)
	x := PanelWidth
	driver.grid[x] = 1
	v.bands = uniformBands(0)
	v.smoothedBands = uniformBands(0)

	now := time.Unix(1, 0)
	v.Tick(VisTickContext{Now: now, Playing: true})
	if !v.DecayPending() {
		t.Fatal("sand was considered settled with a falling grain")
	}
	for i := 1; i <= driver.dotRows && v.DecayPending(); i++ {
		v.Tick(VisTickContext{Now: now.Add(time.Duration(i) * TickFast)})
	}
	if v.DecayPending() {
		t.Fatal("sand remained active after its falling grain landed")
	}
	if got := driver.grid[(driver.dotRows-1)*driver.dotCols+x]; got != 1 {
		t.Fatalf("landed grain = %d, want 1", got)
	}
}

func TestSandFloorErosionNeverSettlesOverAHole(t *testing.T) {
	withPanelWidth(t, 4)
	v := NewVisualizer(44100)
	v.Rows = 5
	activateMode(t, v, VisSand)
	driver := v.driverFor(VisSand).(*sandDriver)
	dotRows, dotCols := v.Rows*4, PanelWidth*2
	driver.ensure(dotRows, dotCols)
	for i := (dotRows - 2) * dotCols; i < len(driver.grid); i++ {
		driver.grid[i] = 1
	}
	v.bands = uniformBands(0)
	v.smoothedBands = uniformBands(0)

	// Silent playback only erodes the floor. Run until the first erosion and
	// check the grains it left unsupported keep the visualizer pending.
	now := time.Unix(1, 0)
	eroded := false
	for i := 0; i < 2000 && !eroded; i++ {
		before := slices.Clone(driver.grid)
		now = now.Add(TickFast)
		v.Tick(VisTickContext{Now: now, Playing: true})
		eroded = !slices.Equal(before, driver.grid)
	}
	if !eroded {
		t.Fatal("floor erosion never removed a grain")
	}
	if !v.DecayPending() {
		t.Fatal("sand reported settled right after eroding a support")
	}

	for i := 0; i < dotRows && v.DecayPending(); i++ {
		now = now.Add(TickFast)
		v.Tick(VisTickContext{Now: now})
	}
	if v.DecayPending() {
		t.Fatal("sand did not settle after erosion")
	}
	for y := 0; y < dotRows-1; y++ {
		for x := range dotCols {
			if driver.grid[y*dotCols+x] != 0 && driver.grid[(y+1)*dotCols+x] == 0 {
				t.Fatalf("settled grain at column %d row %d floats over a hole", x, y)
			}
		}
	}
}

func TestInactiveMosaicWaitsForVisibleCells(t *testing.T) {
	withPanelWidth(t, 8)
	v := NewVisualizer(44100)
	v.Rows = 2
	activateMode(t, v, VisMosaic)
	driver := v.driverFor(VisMosaic).(*mosaicDriver)
	driver.ensureGrid(v.Rows, mosaicTileCount(PanelWidth), DefaultSpectrumBands)
	driver.cells[0].value = 0.06
	v.bands = uniformBands(0)
	v.smoothedBands = uniformBands(0)

	if !v.DecayPending() {
		t.Fatal("mosaic was considered settled with a visible cell")
	}
	v.Tick(VisTickContext{})
	if !v.DecayPending() {
		t.Fatal("mosaic settled while its fading cell was still visible")
	}
	v.Tick(VisTickContext{})
	if v.DecayPending() {
		t.Fatal("mosaic remained active after its cells became invisible")
	}
}

func TestInactiveFlameWaitsForVisibleHeat(t *testing.T) {
	withPanelWidth(t, 4)
	v := NewVisualizer(44100)
	v.Rows = 10
	activateMode(t, v, VisFlame)
	driver := v.driverFor(VisFlame).(*flameDriver)
	for i := range driver.heat {
		driver.heat[i] = 1
	}
	v.bands = uniformBands(0)
	v.smoothedBands = uniformBands(0)

	if !v.DecayPending() {
		t.Fatal("flame was considered settled with visible heat")
	}
	v.Tick(VisTickContext{})
	for x := range driver.dotCols {
		if driver.heat[x] != 0 {
			t.Fatalf("inactive flame source %d = %v, want 0", x, driver.heat[x])
		}
	}
	for i := 1; i < 2*driver.dotRows && v.DecayPending(); i++ {
		v.Tick(VisTickContext{})
	}
	if v.DecayPending() {
		t.Fatal("flame remained active after its heat became invisible")
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
		Playing: true,
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

func TestRawSampleModesAnalyzeEveryTick(t *testing.T) {
	for _, mode := range []VisMode{VisWave, VisScope, VisHeartbeat} {
		t.Run(visModes[mode].name, func(t *testing.T) {
			v := NewVisualizer(44100)
			v.Cols = 80
			activateMode(t, v, mode)

			calls := 0
			ctx := VisTickContext{
				Now:     time.Unix(1, 0),
				Playing: true,
				Analyze: func(spec VisAnalysisSpec) []float64 {
					calls++
					return v.Analyze([]float64{float64(calls)}, spec)
				},
			}
			v.Tick(ctx)
			ctx.Now = ctx.Now.Add(TickWave)
			v.Tick(ctx)

			if calls != 2 {
				t.Fatalf("Analyze calls = %d, want 2", calls)
			}
		})
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

	v.Tick(VisTickContext{Playing: true})
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

	v.Tick(VisTickContext{Playing: true})
	snapshot := append([]float64(nil), driver.buf...)

	v.Render()
	v.Render()

	if !reflect.DeepEqual(driver.buf, snapshot) {
		t.Fatalf("terrain buffer changed across redraws without tick: got %v want %v", driver.buf, snapshot)
	}
}

func TestInactiveTerrainScrollsHistoryToRest(t *testing.T) {
	withPanelWidth(t, 8)

	v := NewVisualizer(44100)
	activateMode(t, v, VisTerrain)
	driver := terrainDriverFor(t, v)
	driver.buf = uniformBandsN(PanelWidth*2, 0.6)
	v.bands = uniformBands(0)
	v.smoothedBands = uniformBands(0)

	if !v.DecayPending() {
		t.Fatal("terrain was considered settled with visible history")
	}
	for range len(driver.buf) / 2 {
		v.Tick(VisTickContext{})
	}
	if v.DecayPending() {
		t.Fatal("terrain remained active after its history scrolled out")
	}
	for i, height := range driver.buf {
		if height != 0 {
			t.Fatalf("terrain column %d = %v after inactive decay, want 0", i, height)
		}
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
