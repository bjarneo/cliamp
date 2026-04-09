package ui

import (
	"math"
	"strings"
	"testing"
	"time"

	"charm.land/lipgloss/v2"
)

const classicPeakTestGlyphs = "⎺⎻⎼⎽"
const classicPeakTestEpsilon = 1e-9

func withPanelWidth(t *testing.T, width int) {
	t.Helper()
	prevWidth := PanelWidth
	PanelWidth = width
	t.Cleanup(func() {
		PanelWidth = prevWidth
	})
}

func uniformBands(level float64) []float64 {
	return uniformBandsN(DefaultSpectrumBands, level)
}

func uniformBandsN(count int, level float64) []float64 {
	bands := make([]float64, count)
	for i := range bands {
		bands[i] = level
	}
	return bands
}

func repeatedClassicPeakSlice(width int, level float64) []float64 {
	cols := classicPeakColsForWidth(width)
	out := make([]float64, cols)
	for i := range out {
		out[i] = level
	}
	return out
}

func activateMode(t *testing.T, v *Visualizer, mode VisMode) visModeDriver {
	t.Helper()
	v.Mode = mode
	driver := v.syncDriverMode()
	if driver == nil {
		t.Fatalf("syncDriverMode() returned nil for mode %v", mode)
	}
	return driver
}

func classicPeakDriverFor(t *testing.T, v *Visualizer) *classicPeakDriver {
	t.Helper()
	driver, ok := v.driverFor(VisClassicPeak).(*classicPeakDriver)
	if !ok || driver == nil {
		t.Fatal("driverFor(VisClassicPeak) did not return *classicPeakDriver")
	}
	return driver
}

func terrainDriverFor(t *testing.T, v *Visualizer) *terrainDriver {
	t.Helper()
	driver, ok := v.driverFor(VisTerrain).(*terrainDriver)
	if !ok || driver == nil {
		t.Fatal("driverFor(VisTerrain) did not return *terrainDriver")
	}
	return driver
}

func TestClassicPeakModeLookup(t *testing.T) {
	if got := StringToVisMode("ClassicPeak"); got != VisClassicPeak {
		t.Fatalf("StringToVisMode(ClassicPeak) = %v, want %v", got, VisClassicPeak)
	}

	v := NewVisualizer(44100)
	v.Mode = VisClassicPeak
	if got := v.ModeName(); got != "ClassicPeak" {
		t.Fatalf("ModeName() = %q, want %q", got, "ClassicPeak")
	}
}

func TestVisualizerFrameAdvancesOnTickNotRender(t *testing.T) {
	v := NewVisualizer(44100)

	v.Analyze(make([]float64, defaultFFTSize), spectrumAnalysisSpec(DefaultSpectrumBands))
	if v.frame != 0 {
		t.Fatalf("Analyze() advanced frame to %d, want 0 before tick", v.frame)
	}

	v.bands = uniformBands(0.5)
	v.Render()
	if v.frame != 0 {
		t.Fatalf("Render() advanced frame to %d, want 0 before tick", v.frame)
	}

	v.Tick(VisTickContext{})
	if v.frame != 1 {
		t.Fatalf("Tick() advanced frame to %d, want 1", v.frame)
	}

	v.bands = uniformBands(0.5)
	v.Render()
	if v.frame != 1 {
		t.Fatalf("Render() advanced frame to %d after tick, want 1", v.frame)
	}
}

func TestClassicPeakLaunchAndSettle(t *testing.T) {
	withPanelWidth(t, 8)
	cols := classicPeakColsForWidth(PanelWidth)

	v := NewVisualizer(44100)
	activateMode(t, v, VisClassicPeak)
	driver := classicPeakDriverFor(t, v)
	v.bands = uniformBands(0.2)
	driver.sync(v)
	v.Render()

	if len(driver.barPos) != cols {
		t.Fatalf("initial bar count = %d, want %d", len(driver.barPos), cols)
	}
	if len(driver.peakPos) != cols {
		t.Fatalf("initial cap count = %d, want %d", len(driver.peakPos), cols)
	}
	for i, pos := range driver.barPos {
		if math.Abs(pos-0.2) > classicPeakTestEpsilon {
			t.Fatalf("initial bar[%d] = %v, want 0.2", i, pos)
		}
	}
	for i, pos := range driver.peakPos {
		if math.Abs(pos-0.2) > classicPeakTestEpsilon {
			t.Fatalf("initial cap[%d] = %v, want 0.2", i, pos)
		}
		if driver.peakVel[i] != 0 {
			t.Fatalf("initial velocity[%d] = %v, want 0", i, driver.peakVel[i])
		}
		if driver.peakHold[i] != 0 {
			t.Fatalf("initial hold[%d] = %v, want 0", i, driver.peakHold[i])
		}
	}

	v.bands = uniformBands(0.8)
	driver.sync(v)
	for i, pos := range driver.barPos {
		if math.Abs(pos-0.2) > classicPeakTestEpsilon {
			t.Fatalf("bars snapped on sync[%d] = %v, want 0.2", i, pos)
		}
	}
	for i, pos := range driver.peakPos {
		if math.Abs(pos-0.8) > classicPeakTestEpsilon {
			t.Fatalf("cap launch[%d] = %v, want 0.8", i, pos)
		}
		if driver.peakVel[i] <= 0 {
			t.Fatalf("velocity on sync[%d] = %v, want > 0", i, driver.peakVel[i])
		}
		if driver.peakHold[i] != 0 {
			t.Fatalf("hold on sync[%d] = %v, want 0", i, driver.peakHold[i])
		}
	}

	now := time.Now()
	driver.advance(v, now)
	for i, pos := range driver.barPos {
		if pos <= 0.2 || pos >= 0.8 {
			t.Fatalf("animated bar[%d] = %v, want between 0.2 and 0.8", i, pos)
		}
		if driver.peakPos[i] <= 0.8 {
			t.Fatalf("animated cap[%d] = %v, want > 0.8", i, driver.peakPos[i])
		}
		if driver.peakPos[i] <= pos {
			t.Fatalf("animated cap[%d] = %v, want > bar %v", i, driver.peakPos[i], pos)
		}
		if driver.peakVel[i] <= 0 {
			t.Fatalf("launch velocity[%d] = %v, want > 0", i, driver.peakVel[i])
		}
	}

	v.bands = uniformBands(0.3)
	driver.sync(v)
	for i, pos := range driver.barPos {
		if pos <= 0.3 {
			t.Fatalf("settling bar[%d] = %v, want > 0.3", i, pos)
		}
		if driver.peakPos[i] <= pos {
			t.Fatalf("settling cap[%d] = %v, want > bar %v", i, driver.peakPos[i], pos)
		}
	}

	for step := 1; step <= 128 && driver.animating(v); step++ {
		driver.advance(v, now.Add(time.Duration(step)*tickClassicPeak))
	}
	if driver.animating(v) {
		t.Fatal("animating() = true after settle window, want false")
	}
	for i, pos := range driver.barPos {
		if math.Abs(pos-0.3) > classicPeakVisibleEpsilon {
			t.Fatalf("settled bar[%d] = %v, want about 0.3", i, pos)
		}
	}
	for i, pos := range driver.peakPos {
		if math.Abs(pos-0.3) > classicPeakVisibleEpsilon {
			t.Fatalf("settled cap[%d] = %v, want about 0.3", i, pos)
		}
		if driver.peakVel[i] != 0 {
			t.Fatalf("settled velocity[%d] = %v, want 0", i, driver.peakVel[i])
		}
		if driver.peakHold[i] != 0 {
			t.Fatalf("settled hold[%d] = %v, want 0", i, driver.peakHold[i])
		}
	}
}

func TestClassicPeakHangsBrieflyAtApex(t *testing.T) {
	withPanelWidth(t, 8)

	v := NewVisualizer(44100)
	activateMode(t, v, VisClassicPeak)
	driver := classicPeakDriverFor(t, v)
	v.bands = uniformBands(0.2)
	driver.sync(v)
	v.Render()

	v.bands = uniformBands(0.8)
	driver.sync(v)

	now := time.Now()
	foundHold := false
	for step := 1; step <= 32; step++ {
		driver.advance(v, now.Add(time.Duration(step)*tickClassicPeak))
		if driver.peakHold[0] <= 0 {
			continue
		}
		foundHold = true
		heldPos := driver.peakPos[0]
		heldVel := driver.peakVel[0]
		heldFor := driver.peakHold[0]

		driver.advance(v, now.Add(time.Duration(step+1)*tickClassicPeak))

		if driver.peakPos[0] != heldPos {
			t.Fatalf("held cap moved: got %v, want %v", driver.peakPos[0], heldPos)
		}
		if driver.peakVel[0] != heldVel {
			t.Fatalf("held cap velocity changed: got %v, want %v", driver.peakVel[0], heldVel)
		}
		if driver.peakHold[0] >= heldFor {
			t.Fatalf("hold timer did not decrease: got %v, want < %v", driver.peakHold[0], heldFor)
		}
		break
	}

	if !foundHold {
		t.Fatal("peakHold never activated")
	}
}

func TestClassicPeakDoesNotRelaunchWhileAirborne(t *testing.T) {
	withPanelWidth(t, 8)

	v := NewVisualizer(44100)
	activateMode(t, v, VisClassicPeak)
	driver := classicPeakDriverFor(t, v)
	v.bands = uniformBands(0.2)
	driver.sync(v)
	v.Render()

	v.bands = uniformBands(0.8)
	driver.sync(v)
	launchPos := append([]float64(nil), driver.peakPos...)
	launchVel := append([]float64(nil), driver.peakVel...)

	v.bands = uniformBands(0.95)
	driver.sync(v)

	for i, pos := range driver.peakPos {
		if pos != launchPos[i] {
			t.Fatalf("cap relaunched in air[%d] = %v, want %v", i, pos, launchPos[i])
		}
		if driver.peakVel[i] != launchVel[i] {
			t.Fatalf("velocity changed in air[%d] = %v, want %v", i, driver.peakVel[i], launchVel[i])
		}
	}
}

func TestClassicPeakResetsOnModeSwitchAndWidthChange(t *testing.T) {
	withPanelWidth(t, 6)
	cols6 := classicPeakColsForWidth(PanelWidth)

	v := NewVisualizer(44100)
	activateMode(t, v, VisClassicPeak)
	driver := classicPeakDriverFor(t, v)
	v.bands = uniformBands(0.4)
	driver.sync(v)
	v.Render()
	driver.barPos[1] = 0.7
	driver.peakPos[1] = 0.9
	driver.peakVel[1] = 1.2

	activateMode(t, v, VisBars)
	activateMode(t, v, VisClassicPeak)
	driver.sync(v)
	if len(driver.barPos) != cols6 {
		t.Fatalf("reset bar len = %d, want %d", len(driver.barPos), cols6)
	}
	if len(driver.peakPos) != cols6 {
		t.Fatalf("reset cap len = %d, want %d", len(driver.peakPos), cols6)
	}
	for i, pos := range driver.barPos {
		if math.Abs(pos-0.4) > classicPeakTestEpsilon {
			t.Fatalf("reset bar[%d] = %v, want 0.4", i, pos)
		}
	}
	for i, pos := range driver.peakPos {
		if math.Abs(pos-0.4) > classicPeakTestEpsilon {
			t.Fatalf("reset cap[%d] = %v, want 0.4", i, pos)
		}
		if driver.peakVel[i] != 0 {
			t.Fatalf("reset velocity[%d] = %v, want 0", i, driver.peakVel[i])
		}
		if driver.peakHold[i] != 0 {
			t.Fatalf("reset hold[%d] = %v, want 0", i, driver.peakHold[i])
		}
	}

	PanelWidth = 8
	cols8 := classicPeakColsForWidth(PanelWidth)
	driver.sync(v)
	if len(driver.barPos) != cols8 {
		t.Fatalf("resize bar len = %d, want %d", len(driver.barPos), cols8)
	}
	if len(driver.peakPos) != cols8 {
		t.Fatalf("resize cap len = %d, want %d", len(driver.peakPos), cols8)
	}
	for i, pos := range driver.barPos {
		if math.Abs(pos-0.4) > classicPeakTestEpsilon {
			t.Fatalf("resize bar[%d] = %v, want 0.4", i, pos)
		}
	}
	for i, pos := range driver.peakPos {
		if math.Abs(pos-0.4) > classicPeakTestEpsilon {
			t.Fatalf("resize cap[%d] = %v, want 0.4", i, pos)
		}
		if driver.peakVel[i] != 0 {
			t.Fatalf("resize velocity[%d] = %v, want 0", i, driver.peakVel[i])
		}
		if driver.peakHold[i] != 0 {
			t.Fatalf("resize hold[%d] = %v, want 0", i, driver.peakHold[i])
		}
	}
}

func TestClassicPeakAnimatingWhenCapIsAboveBar(t *testing.T) {
	withPanelWidth(t, 8)

	v := NewVisualizer(44100)
	activateMode(t, v, VisClassicPeak)
	driver := classicPeakDriverFor(t, v)
	v.bands = uniformBands(0.3)
	driver.barPos = repeatedClassicPeakSlice(PanelWidth, 0.3)
	driver.peakPos = repeatedClassicPeakSlice(PanelWidth, 0.5)
	driver.peakVel = repeatedClassicPeakSlice(PanelWidth, 0)

	if !driver.animating(v) {
		t.Fatal("animating() = false, want true when caps are still above the bar")
	}
}

func TestClassicPeakAnimatingWhenBarsAreSettling(t *testing.T) {
	withPanelWidth(t, 8)

	v := NewVisualizer(44100)
	activateMode(t, v, VisClassicPeak)
	driver := classicPeakDriverFor(t, v)
	v.bands = uniformBands(0.7)
	driver.barPos = repeatedClassicPeakSlice(PanelWidth, 0.5)
	driver.peakPos = repeatedClassicPeakSlice(PanelWidth, 0.5)
	driver.peakVel = repeatedClassicPeakSlice(PanelWidth, 0)

	if !driver.animating(v) {
		t.Fatal("animating() = false, want true while bars are still easing to target")
	}
}

func TestClassicPeakRetainsDetailAtRightEdge(t *testing.T) {
	v := NewVisualizer(44100)
	activateMode(t, v, VisClassicPeak)
	driver := classicPeakDriverFor(t, v)

	bands := make([]float64, classicPeakSpectrumBands)
	copy(bands[len(bands)-6:], []float64{0.55, 0.46, 0.37, 0.28, 0.19, 0.1})
	v.bands = bands

	levels := driver.levels(v)
	if len(levels) < 4 {
		t.Fatalf("levels len = %d, want at least 4", len(levels))
	}

	foundStep := false
	for i := len(levels) - 4; i < len(levels)-1; i++ {
		if math.Abs(levels[i]-levels[i+1]) > 0.01 {
			foundStep = true
			break
		}
	}
	if !foundStep {
		t.Fatalf("right edge flattened unexpectedly: tail=%v", levels[len(levels)-4:])
	}
}

func TestClassicPeakRenderHidesLandedCaps(t *testing.T) {
	withPanelWidth(t, 8)

	v := NewVisualizer(44100)
	activateMode(t, v, VisClassicPeak)
	v.Rows = 5
	v.bands = uniformBands(0.6)

	out := v.Render()
	if strings.ContainsAny(out, classicPeakTestGlyphs) {
		t.Fatalf("Render() showed a cap even though peaks were seeded at the bar height: %q", out)
	}
}

func TestClassicPeakRenderShowsAttachedCapWhileSettling(t *testing.T) {
	withPanelWidth(t, 8)

	v := NewVisualizer(44100)
	activateMode(t, v, VisClassicPeak)
	driver := classicPeakDriverFor(t, v)
	v.Rows = 5
	v.bands = uniformBands(0.61)
	driver.barPos = repeatedClassicPeakSlice(PanelWidth, 0.61)
	driver.peakPos = repeatedClassicPeakSlice(PanelWidth, 0.68)
	driver.peakVel = repeatedClassicPeakSlice(PanelWidth, 0)

	out := v.Render()
	if !strings.ContainsAny(out, classicPeakTestGlyphs) {
		t.Fatalf("Render() did not show an attached settling cap: %q", out)
	}
}

func TestClassicPeakPauseFreezesStateAndClearsAnimationClock(t *testing.T) {
	withPanelWidth(t, 8)

	v := NewVisualizer(44100)
	activateMode(t, v, VisClassicPeak)
	driver := classicPeakDriverFor(t, v)
	v.Rows = 5
	v.bands = uniformBands(0.6)
	driver.barPos = repeatedClassicPeakSlice(PanelWidth, 0.6)
	driver.peakPos = repeatedClassicPeakSlice(PanelWidth, 0.82)
	driver.peakVel = repeatedClassicPeakSlice(PanelWidth, 1.1)
	driver.peakHold = repeatedClassicPeakSlice(PanelWidth, classicPeakApexHold)

	snapshotPeak := append([]float64(nil), driver.peakPos...)
	snapshotVel := append([]float64(nil), driver.peakVel...)
	snapshotHold := append([]float64(nil), driver.peakHold...)

	driver.lastTick = time.Now()
	driver.Tick(v, VisTickContext{Now: time.Now(), Paused: true})

	if !driver.lastTick.IsZero() {
		t.Fatalf("lastTick after pause = %v, want zero", driver.lastTick)
	}
	for i := range driver.peakPos {
		if driver.peakPos[i] != snapshotPeak[i] {
			t.Fatalf("paused cap[%d] = %v, want frozen %v", i, driver.peakPos[i], snapshotPeak[i])
		}
		if driver.peakVel[i] != snapshotVel[i] {
			t.Fatalf("paused velocity[%d] = %v, want frozen %v", i, driver.peakVel[i], snapshotVel[i])
		}
		if driver.peakHold[i] != snapshotHold[i] {
			t.Fatalf("paused hold[%d] = %v, want frozen %v", i, driver.peakHold[i], snapshotHold[i])
		}
	}
	if !driver.animating(v) {
		t.Fatal("animating() = false, want true while caps still airborne")
	}
	if got := v.TickInterval(VisTickContext{Paused: true}); got != TickSlow {
		t.Fatalf("TickInterval(paused) = %v, want %v", got, TickSlow)
	}
}

func TestClassicPeakOverlayFreezesStateAndClearsAnimationClock(t *testing.T) {
	withPanelWidth(t, 8)

	v := NewVisualizer(44100)
	activateMode(t, v, VisClassicPeak)
	driver := classicPeakDriverFor(t, v)
	v.Rows = 5
	v.bands = uniformBands(0.6)
	driver.barPos = repeatedClassicPeakSlice(PanelWidth, 0.6)
	driver.peakPos = repeatedClassicPeakSlice(PanelWidth, 0.82)
	driver.peakVel = repeatedClassicPeakSlice(PanelWidth, 1.1)
	driver.peakHold = repeatedClassicPeakSlice(PanelWidth, classicPeakApexHold)

	snapshotPeak := append([]float64(nil), driver.peakPos...)
	snapshotVel := append([]float64(nil), driver.peakVel...)
	snapshotHold := append([]float64(nil), driver.peakHold...)

	driver.lastTick = time.Now()
	driver.Tick(v, VisTickContext{Now: time.Now(), OverlayActive: true})

	if !driver.lastTick.IsZero() {
		t.Fatalf("lastTick after overlay = %v, want zero", driver.lastTick)
	}
	for i := range driver.peakPos {
		if driver.peakPos[i] != snapshotPeak[i] {
			t.Fatalf("overlay cap[%d] = %v, want frozen %v", i, driver.peakPos[i], snapshotPeak[i])
		}
		if driver.peakVel[i] != snapshotVel[i] {
			t.Fatalf("overlay velocity[%d] = %v, want frozen %v", i, driver.peakVel[i], snapshotVel[i])
		}
		if driver.peakHold[i] != snapshotHold[i] {
			t.Fatalf("overlay hold[%d] = %v, want frozen %v", i, driver.peakHold[i], snapshotHold[i])
		}
	}
	if !driver.animating(v) {
		t.Fatal("animating() = false, want true while overlay hides airborne caps")
	}
	if got := v.TickInterval(VisTickContext{OverlayActive: true}); got != TickSlow {
		t.Fatalf("TickInterval(overlay) = %v, want %v", got, TickSlow)
	}
}

func TestClassicPeakRenderFillsEvenWidthPanels(t *testing.T) {
	withPanelWidth(t, 8)

	v := NewVisualizer(44100)
	activateMode(t, v, VisClassicPeak)
	v.Rows = 5
	v.bands = uniformBands(0.6)

	out := v.Render()
	for line := range strings.SplitSeq(out, "\n") {
		if got := lipgloss.Width(line); got != PanelWidth {
			t.Fatalf("Render() line width = %d, want %d for even panel width: %q", got, PanelWidth, line)
		}
	}
}

func TestClassicPeakEvenWidthKeepsBarsSeparated(t *testing.T) {
	withPanelWidth(t, 8)

	v := NewVisualizer(44100)
	activateMode(t, v, VisClassicPeak)
	v.Rows = 5
	v.bands = uniformBands(0.6)

	out := strings.Split(v.Render(), "\n")
	if len(out) == 0 {
		t.Fatalf("Render() returned too few rows: %q", out)
	}
	found := false
	for _, line := range out {
		if strings.Contains(line, "█") {
			if strings.Contains(line, "███") {
				t.Fatalf("Render() merged adjacent bars on even widths: %q", line)
			}
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("Render() did not render any bar rows: %q", out)
	}
}
