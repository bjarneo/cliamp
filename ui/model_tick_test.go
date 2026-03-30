package ui

import (
	"os"
	"testing"
	"time"

	"cliamp/player"
	"cliamp/playlist"
	tea "github.com/charmbracelet/bubbletea"
)

var sharedPlayer *player.Player

func TestMain(m *testing.M) {
	sr := player.DeviceSampleRate()
	if sr <= 0 {
		sr = 44100
	}
	p, err := player.New(player.Quality{SampleRate: sr, BufferMs: 100, ResampleQuality: 1})
	if err == nil {
		sharedPlayer = p
		defer p.Close()
	}
	os.Exit(m.Run())
}

// TestTickIntervalStoppedUsesSlow verifies that when the player is stopped,
// the tick interval is tickSlow (~200ms) not tickFast (~50ms), regardless of
// the visualizer mode. This matters for CPU usage (issue #92).
func TestTickIntervalStoppedUsesSlow(t *testing.T) {
	if sharedPlayer == nil {
		t.Skip("audio hardware unavailable")
	}
	m := Model{
		player:    sharedPlayer,
		vis:       NewVisualizer(float64(sharedPlayer.SampleRate())),
		playlist:  playlist.New(),
		termTitle: terminalTitleState{last: baseTerminalTitle},
	}

	// Player is stopped by default (IsPlaying=false).
	// vis.Mode defaults to VisBars (0 != VisNone).
	if sharedPlayer.IsPlaying() {
		t.Fatal("expected player to be stopped")
	}
	if m.vis.Mode == VisNone {
		t.Fatal("expected default vis mode to be non-None (VisBars)")
	}

	_, cmd := m.Update(tickMsg(time.Now()))
	if cmd == nil {
		t.Fatal("tickMsg returned nil cmd")
	}

	start := time.Now()
	cmd() // blocks until the tick timer fires
	elapsed := time.Since(start)

	// tickSlow=200ms, tickFast=50ms. With tolerance for scheduling jitter.
	const tolerance = 80 * time.Millisecond
	if elapsed < tickSlow-tolerance {
		t.Errorf("tick fired after %v, want ~%v (tickSlow); got tickFast instead — CPU fix not working",
			elapsed, tickSlow)
	}
	t.Logf("tick interval when stopped: %v (want ~%v tickSlow)", elapsed.Round(time.Millisecond), tickSlow)
}

func TestTickIntervalForState(t *testing.T) {
	tests := []struct {
		name        string
		introActive bool
		visualizer  bool
		playing     bool
		paused      bool
		want        time.Duration
	}{
		{name: "intro", introActive: true, want: tickFast},
		{name: "playing with visualizer", visualizer: true, playing: true, want: tickFast},
		{name: "playing without visualizer", playing: true, want: tickSlow},
		{name: "paused", playing: true, paused: true, want: tickSlow},
		{name: "stopped", want: tickSlow},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tickIntervalForState(tt.introActive, tt.visualizer, tt.playing, tt.paused); got != tt.want {
				t.Fatalf("tickIntervalForState(%v, %v, %v, %v) = %v, want %v",
					tt.introActive, tt.visualizer, tt.playing, tt.paused, got, tt.want)
			}
		})
	}
}

func TestInitialTickUsesFastCadence(t *testing.T) {
	prev := teaTick
	t.Cleanup(func() {
		teaTick = prev
	})

	called := false
	teaTick = func(d time.Duration, fn func(time.Time) tea.Msg) tea.Cmd {
		called = true
		if d != tickFast {
			t.Fatalf("tick duration = %v, want %v", d, tickFast)
		}
		return func() tea.Msg {
			return fn(time.Unix(0, 0))
		}
	}

	msg := tickCmd()()

	if _, ok := msg.(tickMsg); !ok {
		t.Fatalf("tickCmd() message = %T, want tickMsg", msg)
	}
	if !called {
		t.Fatal("tickCmd() did not schedule teaTick")
	}
}

func TestTickIntervalClassicPeakSettlingUsesAdaptiveCadence(t *testing.T) {
	m := Model{
		vis:      NewVisualizer(44100),
		playlist: playlist.New(),
	}
	activateMode(t, m.vis, VisClassicPeak)
	driver := classicPeakDriverFor(t, m.vis)
	m.vis.Rows = defaultVisRows
	m.vis.bands = uniformBands(0.3)
	driver.barPos = repeatedClassicPeakSlice(8, 0.3)
	driver.peakPos = repeatedClassicPeakSlice(8, 0.5)
	driver.peakVel = repeatedClassicPeakSlice(8, 0)

	withPanelWidth(t, 8)

	if !driver.animating(m.vis) {
		t.Fatal("animating() = false, want true while ClassicPeak caps are still settling")
	}

	wantFPS := classicPeakLaunchMax * float64(defaultVisRows*len(classicPeakGlyphs))
	wantFPS = min(classicPeakMaxFPS, max(classicPeakMinFPS, wantFPS))
	want := time.Duration(float64(time.Second) / wantFPS)
	if got := m.tickInterval(); got != want {
		t.Fatalf("tickInterval() = %v, want %v while ClassicPeak caps are still settling", got, want)
	}
}

func TestClassicPeakAnalysisIntervalUsesFFTOverlapLimit(t *testing.T) {
	m := Model{
		vis: NewVisualizer(44100),
	}
	activateMode(t, m.vis, VisClassicPeak)
	driver := classicPeakDriverFor(t, m.vis)
	m.vis.Rows = 24

	frame := driver.frameInterval(m.vis)
	if frame != tickClassicPeak {
		t.Fatalf("frameInterval() = %v, want %v when rows clamp to max FPS", frame, tickClassicPeak)
	}

	spec := driver.AnalysisSpec(m.vis)
	window := time.Duration(float64(time.Second) * float64(spec.FFTSize) / m.vis.sr)
	want := max(frame, max(classicPeakSampleFloor, time.Duration(float64(window)/classicPeakFFTOverlap)))
	if got := driver.analysisInterval(m.vis); got != want {
		t.Fatalf("analysisInterval() = %v, want %v", got, want)
	}
}

func TestTickClassicPeakStoppedDecayKeepsAnimatingTowardSilence(t *testing.T) {
	v := NewVisualizer(44100)
	activateMode(t, v, VisClassicPeak)
	driver := classicPeakDriverFor(t, v)
	v.Rows = defaultVisRows
	spec := driver.AnalysisSpec(v)
	v.prevBySpec[spec] = uniformBandsN(spec.BandCount, 0.6)
	v.bands = uniformBandsN(spec.BandCount, 0.6)
	driver.barPos = repeatedClassicPeakSlice(8, 0.6)
	driver.peakPos = repeatedClassicPeakSlice(8, 0.6)
	driver.peakVel = repeatedClassicPeakSlice(8, 0)
	driver.peakHold = repeatedClassicPeakSlice(8, 0)

	withPanelWidth(t, 8)

	calls := 0
	driver.Tick(v, visTickContext{
		Now: time.Now(),
		Analyze: func(visAnalysisSpec) []float64 {
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

func TestRefreshVisualizerIfPendingConsumesOneShotRequest(t *testing.T) {
	m := Model{
		vis: NewVisualizer(44100),
	}

	m.vis.requestRefresh()
	m.refreshVisualizerIfPending()

	if m.vis.refreshPending {
		t.Fatal("refreshPending = true after refreshVisualizerIfPending(), want false")
	}
	if m.vis.frame != 1 {
		t.Fatalf("frame after refreshVisualizerIfPending() = %d, want 1", m.vis.frame)
	}

	m.refreshVisualizerIfPending()
	if m.vis.frame != 1 {
		t.Fatalf("frame after second refreshVisualizerIfPending() = %d, want 1", m.vis.frame)
	}
}

func TestLyricsScreenHidesVisualizerTicks(t *testing.T) {
	m := Model{
		vis: NewVisualizer(44100),
		lyrics: lyricsState{
			visible: true,
		},
	}

	if got := m.activeScreen(); got != screenLyrics {
		t.Fatalf("activeScreen() = %v, want %v", got, screenLyrics)
	}
	if !m.isOverlayActive() {
		t.Fatal("isOverlayActive() = false, want true while lyrics screen is visible")
	}
	if !m.visualizerTickContext(time.Now()).OverlayActive {
		t.Fatal("visualizerTickContext(...).OverlayActive = false, want true for lyrics screen")
	}

	m.vis.requestRefresh()
	m.refreshVisualizerIfPending()

	if !m.vis.refreshPending {
		t.Fatal("refreshPending = false after lyrics-screen refresh attempt, want true")
	}
	if m.vis.frame != 0 {
		t.Fatalf("frame after lyrics-screen refresh attempt = %d, want 0", m.vis.frame)
	}
}

func TestUpdateRequestsVisualizerRefreshWhenOverlayCloses(t *testing.T) {
	m := Model{
		vis: NewVisualizer(44100),
		keymap: keymapOverlay{
			visible: true,
		},
	}

	nextModel, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEscape})
	if cmd != nil {
		t.Fatalf("Update() cmd = %v, want nil", cmd)
	}

	next, ok := nextModel.(Model)
	if !ok {
		t.Fatalf("Update() model = %T, want ui.Model", nextModel)
	}
	if next.keymap.visible {
		t.Fatal("keymap overlay remained visible after escape")
	}
	if !next.vis.refreshPending {
		t.Fatal("refreshPending = false after overlay close, want true")
	}
}

func TestUpdateRequestsVisualizerRefreshWhenLyricsClose(t *testing.T) {
	m := Model{
		vis: NewVisualizer(44100),
		lyrics: lyricsState{
			visible: true,
		},
	}

	nextModel, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEscape})
	if cmd != nil {
		t.Fatalf("Update() cmd = %v, want nil", cmd)
	}

	next, ok := nextModel.(Model)
	if !ok {
		t.Fatalf("Update() model = %T, want ui.Model", nextModel)
	}
	if next.lyrics.visible {
		t.Fatal("lyrics overlay remained visible after escape")
	}
	if !next.vis.refreshPending {
		t.Fatal("refreshPending = false after lyrics close, want true")
	}
}

func TestAdvanceTickUnitsClearsElapsedWhenCounterCompletes(t *testing.T) {
	ttl := 1
	elapsed := time.Duration(0)

	if got := advanceTickUnits(&ttl, &elapsed, 3*time.Second, tickFast); got != 1 {
		t.Fatalf("advanceTickUnits() steps = %d, want 1", got)
	}
	if ttl != 0 {
		t.Fatalf("ttl after completion = %d, want 0", ttl)
	}
	if elapsed != 0 {
		t.Fatalf("elapsed after completion = %v, want 0", elapsed)
	}
}
