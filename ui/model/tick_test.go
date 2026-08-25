package model

import (
	"math"
	"os"
	"testing"
	"time"

	"github.com/bjarneo/cliamp/player"
	"github.com/bjarneo/cliamp/playlist"
	"github.com/bjarneo/cliamp/ui"

	tea "charm.land/bubbletea/v2"
)

var sharedPlayer player.Engine

type stereoFakeEngine struct {
	*playbackFakeEngine
	samples [][2]float64
	volume  float64
	mono    bool
}

func (f *stereoFakeEngine) StereoSamplesInto(dst [][2]float64) int {
	return copy(dst, f.samples)
}

func (f *stereoFakeEngine) Volume() float64 { return f.volume }
func (f *stereoFakeEngine) Mono() bool      { return f.mono }

type samplingFakeEngine struct {
	*playbackFakeEngine
	sampleCalls         int
	waveformSampleCalls int
	stereoSampleCalls   int
}

func (f *samplingFakeEngine) SamplesInto(dst []float64) int {
	f.sampleCalls++
	return f.fillSamples(dst)
}

func (f *samplingFakeEngine) WaveformSamplesInto(dst []float64) int {
	f.waveformSampleCalls++
	return f.fillSamples(dst)
}

func (f *samplingFakeEngine) fillSamples(dst []float64) int {
	if len(dst) == 0 {
		return 0
	}
	n := min(512, len(dst))
	for i := range n {
		dst[i] = 0.8 * math.Sin(2*math.Pi*1000*float64(i)/float64(f.SampleRate()))
	}
	return n
}

func (f *samplingFakeEngine) StereoSamplesInto(dst [][2]float64) int {
	f.stereoSampleCalls++
	if len(dst) == 0 {
		return 0
	}
	dst[0] = [2]float64{1, 1}
	return 1
}

func TestMain(m *testing.M) {
	os.Unsetenv("CLIAMP_CONFIG_DIR")
	os.Unsetenv("XDG_CONFIG_HOME")

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

// TestTickIntervalStoppedUsesIdle verifies that a fully-idle model (player
// stopped, no overlay, no pending status / reconnect) ticks at ui.TickIdle
// rather than ui.TickSlow / ui.TickFast. This is what lets the CPU sit in a
// low P-state between user actions (issue #92 and follow-ups).
func TestTickIntervalStoppedUsesIdle(t *testing.T) {
	if sharedPlayer == nil {
		t.Skip("audio hardware unavailable")
	}
	m := Model{
		player:    sharedPlayer,
		vis:       ui.NewVisualizer(float64(sharedPlayer.SampleRate())),
		playlist:  playlist.New(),
		termTitle: terminalTitleState{},
	}

	if sharedPlayer.IsPlaying() {
		t.Fatal("expected player to be stopped")
	}
	if !m.isFullyIdle() {
		t.Fatal("isFullyIdle() = false on a fresh stopped model, want true")
	}
	if got := m.tickInterval(); got != ui.TickIdle {
		t.Errorf("tickInterval() = %v, want %v (ui.TickIdle)", got, ui.TickIdle)
	}
}

// TestTickIntervalPendingStatusUsesSlow verifies that a pending status
// message keeps us off the idle cadence — otherwise the message would linger
// up to TickIdle past its expiry.
func TestTickIntervalPendingStatusUsesSlow(t *testing.T) {
	if sharedPlayer == nil {
		t.Skip("audio hardware unavailable")
	}
	m := Model{
		player:   sharedPlayer,
		vis:      ui.NewVisualizer(float64(sharedPlayer.SampleRate())),
		playlist: playlist.New(),
	}
	m.status.Show("hello", statusTTL(2*time.Second))

	if m.isFullyIdle() {
		t.Fatal("isFullyIdle() = true with pending status message, want false")
	}
	if got := m.tickInterval(); got == ui.TickIdle {
		t.Errorf("tickInterval() = %v with pending status, want a faster cadence", got)
	}
}

func TestTickIntervalPlayingUsesFastCadence(t *testing.T) {
	p := &playbackFakeEngine{playing: true}
	m := Model{
		player:   p,
		vis:      ui.NewVisualizer(float64(p.SampleRate())),
		playlist: playlist.New(),
		width:    80,
		height:   24,
	}
	m.recomputeLayout()
	m.SetVisualizer("none")

	if got := m.tickInterval(); got != ui.TickFast {
		t.Fatalf("tickInterval() = %v, want %v", got, ui.TickFast)
	}
}

func TestTickIntervalClampsFastVisualizerCadence(t *testing.T) {
	p := &playbackFakeEngine{playing: true}
	m := Model{
		player:   p,
		vis:      ui.NewVisualizer(float64(p.SampleRate())),
		playlist: playlist.New(),
		width:    80,
		height:   24,
	}
	m.recomputeLayout()

	if got := m.vis.TickInterval(m.visualizerTickContext(time.Time{})); got >= ui.TickFast {
		t.Fatalf("visualizer tick interval = %v, want faster than %v for test setup", got, ui.TickFast)
	}
	if got := m.tickInterval(); got != ui.TickFast {
		t.Fatalf("tickInterval() = %v, want %v", got, ui.TickFast)
	}
}

func TestTickIntervalVisualizer60FPS(t *testing.T) {
	p := &playbackFakeEngine{playing: true}
	m := Model{
		player:   p,
		vis:      ui.NewVisualizer(float64(p.SampleRate())),
		playlist: playlist.New(),
		width:    80,
		height:   24,
	}
	m.recomputeLayout()
	m.SetVisualizer60FPS(true)

	if got := m.tickInterval(); got != ui.TickAnim {
		t.Fatalf("tickInterval() = %v, want %v", got, ui.TickAnim)
	}

	m.SetLowPower(true)
	if got := m.tickInterval(); got != ui.TickLowPowerPlaying {
		t.Fatalf("tickInterval() with low-power = %v, want %v", got, ui.TickLowPowerPlaying)
	}
}

func TestTickIntervalRawSampleVisualizerUsesWaveCadence(t *testing.T) {
	p := &playbackFakeEngine{playing: true}
	m := Model{
		player:   p,
		vis:      ui.NewVisualizer(float64(p.SampleRate())),
		playlist: playlist.New(),
		width:    80,
		height:   24,
	}
	m.recomputeLayout()
	m.SetVisualizer("Wave")

	if got := m.tickInterval(); got != ui.TickWave {
		t.Fatalf("Wave tickInterval() = %v, want %v", got, ui.TickWave)
	}

	m.SetVisualizer("Heartbeat")
	if got := m.tickInterval(); got != ui.TickWave {
		t.Fatalf("Heartbeat tickInterval() = %v, want %v", got, ui.TickWave)
	}
}

func TestRawSampleVisualizerUsesWaveformTap(t *testing.T) {
	p := &samplingFakeEngine{playbackFakeEngine: &playbackFakeEngine{playing: true}}
	m := Model{
		player:   p,
		vis:      ui.NewVisualizer(float64(p.SampleRate())),
		playlist: playlist.New(),
		width:    80,
		height:   24,
	}
	m.recomputeLayout()
	m.SetVisualizer("Wave")
	m.tickVisualizer(time.Unix(1, 0))

	if p.waveformSampleCalls != 1 {
		t.Fatalf("WaveformSamplesInto calls = %d, want 1", p.waveformSampleCalls)
	}
	if p.sampleCalls != 0 {
		t.Fatalf("SamplesInto calls = %d, want 0", p.sampleCalls)
	}
}

func TestTickIntervalLowPowerPlayingUsesLowPowerCadence(t *testing.T) {
	p := &playbackFakeEngine{playing: true}
	m := Model{
		player:   p,
		vis:      ui.NewVisualizer(float64(p.SampleRate())),
		playlist: playlist.New(),
	}
	m.SetLowPower(true)

	if got := m.tickInterval(); got != ui.TickLowPowerPlaying {
		t.Fatalf("tickInterval() = %v, want %v", got, ui.TickLowPowerPlaying)
	}
}

func chargedPausedModel(t *testing.T) (Model, *samplingFakeEngine) {
	t.Helper()
	p := &samplingFakeEngine{playbackFakeEngine: &playbackFakeEngine{playing: true}}
	m := Model{
		player:   p,
		vis:      ui.NewVisualizer(float64(p.SampleRate())),
		playlist: playlist.New(),
		width:    80,
		height:   24,
	}
	m.recomputeLayout()
	m.vis.Mode = ui.VisBars

	// Charge the bars through the normal tick path so v.bands is populated,
	// then pause to leave non-empty spectrum content behind.
	base := time.Unix(1, 0)
	for i := range 3 {
		m.tickVisualizer(base.Add(time.Duration(i+1) * ui.TickFast))
	}
	p.paused = true
	return m, p
}

// TestTickIntervalPausedSettlingVisualizerUsesFast verifies that pausing with
// non-empty spectrum content keeps a fast (non-idle) cadence so the bars ease
// down smoothly instead of freezing or stepping at a few fps, then returns to
// idle once they settle.
func TestTickIntervalPausedSettlingVisualizerUsesFast(t *testing.T) {
	m, _ := chargedPausedModel(t)

	if !m.isOverlayActive() && !m.visualizerSettlingPaused() {
		t.Fatal("visualizerSettlingPaused() = false with charged paused bars, want true")
	}
	if m.isFullyIdle() {
		t.Fatal("isFullyIdle() = true while paused bars settle, want false")
	}
	if got := m.tickInterval(); got != ui.TickFast {
		t.Fatalf("tickInterval() = %v, want %v while paused bars settle", got, ui.TickFast)
	}
}

func TestTickIntervalPausedSettledVisualizerUsesIdle(t *testing.T) {
	m, _ := chargedPausedModel(t)

	base := time.Unix(1, 0)
	for i := range 120 {
		m.tickVisualizer(base.Add(time.Duration(i+1) * ui.TickSlow))
		if !m.visualizerSettlingPaused() {
			break
		}
	}
	if m.visualizerSettlingPaused() {
		t.Fatal("visualizer still settling after decay ticks")
	}
	if !m.isFullyIdle() {
		t.Fatal("isFullyIdle() = false after paused bars settled, want true")
	}
	if got := m.tickInterval(); got != ui.TickIdle {
		t.Fatalf("tickInterval() = %v, want %v after paused bars settle", got, ui.TickIdle)
	}
}

func TestVisualizerVisibility(t *testing.T) {
	tests := []struct {
		name          string
		width, height int
		set           func(*Model)
		zeroRows      bool
		want          bool
	}{
		{name: "main", width: 80, height: 24, set: func(*Model) {}, want: true},
		{name: "content first", width: 80, height: 24, set: func(m *Model) { m.keymap.visible = true }, want: false},
		{name: "picker preview", width: 80, height: 24, set: func(m *Model) { m.visPicker.visible = true }, want: true},
		{name: "full visualizer", width: 40, height: 10, set: func(m *Model) { m.fullVis = true }, want: true},
		{name: "minimal", width: 40, height: 10, set: func(*Model) {}, want: false},
		{name: "too small", width: 39, height: 9, set: func(*Model) {}, want: false},
		{name: "disabled", width: 80, height: 24, set: func(m *Model) { m.vis.Mode = ui.VisNone }, want: false},
		{name: "zero row canvas", width: 80, height: 24, set: func(*Model) {}, zeroRows: true, want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := Model{vis: ui.NewVisualizer(44100), width: tt.width, height: tt.height}
			tt.set(&m)
			m.recomputeLayout()
			if tt.zeroRows {
				m.vis.Rows = 0
			}
			if got := m.visualizerVisible(); got != tt.want {
				t.Fatalf("visualizerVisible() = %t, want %t", got, tt.want)
			}
		})
	}
}

func TestTickVisualizerSkipsContentFirstLayouts(t *testing.T) {
	tests := []struct {
		name            string
		mode            ui.VisMode
		wantSamples     int
		wantStereoCalls int
	}{
		{name: "FFT", mode: ui.VisBars, wantSamples: 1},
		{name: "stereo", mode: ui.VisStereo, wantStereoCalls: 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := &samplingFakeEngine{playbackFakeEngine: &playbackFakeEngine{playing: true}}
			m := Model{
				player: p,
				vis:    ui.NewVisualizer(float64(p.SampleRate())),
				keymap: keymapOverlay{visible: true},
				width:  80,
				height: 24,
			}
			m.vis.Mode = tt.mode
			m.recomputeLayout()

			m.tickVisualizer(time.Now())
			if p.sampleCalls != 0 || p.stereoSampleCalls != 0 {
				t.Fatalf("hidden visualizer sampled audio: mono=%d stereo=%d", p.sampleCalls, p.stereoSampleCalls)
			}
			if got := m.vis.Frame(); got != 0 {
				t.Fatalf("hidden visualizer frame = %d, want 0", got)
			}

			m.keymap.visible = false
			m.recomputeLayout()
			m.tickVisualizer(time.Now())
			if p.sampleCalls != tt.wantSamples || p.stereoSampleCalls != tt.wantStereoCalls {
				t.Fatalf("visible visualizer samples: mono=%d stereo=%d, want mono=%d stereo=%d", p.sampleCalls, p.stereoSampleCalls, tt.wantSamples, tt.wantStereoCalls)
			}
			if got := m.vis.Frame(); got != 1 {
				t.Fatalf("visible visualizer frame = %d, want 1", got)
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
		if d != ui.TickFast {
			t.Fatalf("tick duration = %v, want %v", d, ui.TickFast)
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

func TestVisualizerTickContextProcessesStereoOutput(t *testing.T) {
	player := &stereoFakeEngine{
		playbackFakeEngine: &playbackFakeEngine{playing: true},
		samples:            [][2]float64{{0.8, 0.4}},
		volume:             -6.020599913279624,
		mono:               true,
	}
	m := Model{
		player:          player,
		vis:             ui.NewVisualizer(44100),
		visVolumeLinked: true,
	}
	m.vis.Mode = ui.VisStereo

	dst := make([][2]float64, 1)
	n := m.visualizerTickContext(time.Now()).StereoSamplesInto(dst)
	if n != 1 {
		t.Fatalf("StereoSamplesInto() = %d, want 1", n)
	}
	want := 0.3
	for channel, got := range dst[0] {
		if math.Abs(got-want) > 1e-9 {
			t.Errorf("sample channel %d = %v, want %v", channel, got, want)
		}
	}
}

func TestRefreshVisualizerIfPendingConsumesOneShotRequest(t *testing.T) {
	m := Model{
		vis:    ui.NewVisualizer(44100),
		width:  80,
		height: 24,
	}
	m.recomputeLayout()

	m.vis.RequestRefresh()
	m.refreshVisualizerIfPending()

	if m.vis.RefreshPending() {
		t.Fatal("refreshPending = true after refreshVisualizerIfPending(), want false")
	}
	if m.vis.Frame() != 1 {
		t.Fatalf("frame after refreshVisualizerIfPending() = %d, want 1", m.vis.Frame())
	}

	m.refreshVisualizerIfPending()
	if m.vis.Frame() != 1 {
		t.Fatalf("frame after second refreshVisualizerIfPending() = %d, want 1", m.vis.Frame())
	}
}

func TestLyricsScreenKeepsVisualizerLive(t *testing.T) {
	m := Model{
		vis:    ui.NewVisualizer(44100),
		width:  80,
		height: 24,
		lyrics: lyricsState{
			visible: true,
		},
	}
	m.recomputeLayout()

	if got := m.activeScreen(); got != screenLyrics {
		t.Fatalf("activeScreen() = %v, want %v", got, screenLyrics)
	}
	// Overlays now render inline over the live main view, so the visualizer is
	// never treated as hidden.
	if m.isOverlayActive() {
		t.Fatal("isOverlayActive() = true, want false: overlays render inline")
	}
	if m.visualizerTickContext(time.Now()).OverlayActive {
		t.Fatal("visualizerTickContext(...).OverlayActive = true, want false for inline lyrics")
	}

	m.vis.RequestRefresh()
	m.refreshVisualizerIfPending()

	if m.vis.RefreshPending() {
		t.Fatal("refreshPending = true after refresh, want false: visualizer stays live under lyrics")
	}
	if m.vis.Frame() != 1 {
		t.Fatalf("frame after refresh = %d, want 1 while lyrics open", m.vis.Frame())
	}
}

func TestUpdateRequestsVisualizerRefreshWhenOverlayCloses(t *testing.T) {
	m := Model{
		vis: ui.NewVisualizer(44100),
		keymap: keymapOverlay{
			visible: true,
		},
	}

	nextModel, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	if cmd != nil {
		t.Fatalf("Update() cmd = %v, want nil", cmd)
	}

	next, ok := nextModel.(Model)
	if !ok {
		t.Fatalf("Update() model = %T, want Model", nextModel)
	}
	if next.keymap.visible {
		t.Fatal("keymap overlay remained visible after escape")
	}
	if !next.vis.RefreshPending() {
		t.Fatal("refreshPending = false after overlay close, want true")
	}
}

func TestUpdateRequestsVisualizerRefreshWhenLyricsClose(t *testing.T) {
	m := Model{
		vis: ui.NewVisualizer(44100),
		lyrics: lyricsState{
			visible: true,
		},
	}

	nextModel, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	if cmd != nil {
		t.Fatalf("Update() cmd = %v, want nil", cmd)
	}

	next, ok := nextModel.(Model)
	if !ok {
		t.Fatalf("Update() model = %T, want Model", nextModel)
	}
	if next.lyrics.visible {
		t.Fatal("lyrics overlay remained visible after escape")
	}
	if !next.vis.RefreshPending() {
		t.Fatal("refreshPending = false after lyrics close, want true")
	}
}

func TestAdvanceTickUnitsClearsElapsedWhenCounterCompletes(t *testing.T) {
	ttl := 1
	elapsed := time.Duration(0)

	if got := advanceTickUnits(&ttl, &elapsed, 3*time.Second, ui.TickFast); got != 1 {
		t.Fatalf("advanceTickUnits() steps = %d, want 1", got)
	}
	if ttl != 0 {
		t.Fatalf("ttl after completion = %d, want 0", ttl)
	}
	if elapsed != 0 {
		t.Fatalf("elapsed after completion = %v, want 0", elapsed)
	}
}
