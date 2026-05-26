package model

import (
	"os"
	"testing"
	"time"

	"cliamp/player"
	"cliamp/playlist"
	"cliamp/ui"

	tea "charm.land/bubbletea/v2"
)

var sharedPlayer player.Engine

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
	}
	m.SetVisualizer("none")

	if got := m.tickInterval(); got != ui.TickFast {
		t.Fatalf("tickInterval() = %v, want %v", got, ui.TickFast)
	}
}

func TestTickIntervalLowPowerPlayingUsesLowPowerCadence(t *testing.T) {
	p := &playbackFakeEngine{playing: true}
	m := Model{
		player:   p,
		vis:      ui.NewVisualizer(float64(p.SampleRate())),
		playlist: playlist.New(),
	}
	m.SetVisualizer("none")
	m.SetLowPower(true)

	if got := m.tickInterval(); got != ui.TickLowPowerPlaying {
		t.Fatalf("tickInterval() = %v, want %v", got, ui.TickLowPowerPlaying)
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

func TestRefreshVisualizerIfPendingConsumesOneShotRequest(t *testing.T) {
	m := Model{
		vis: ui.NewVisualizer(44100),
	}

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

func TestLyricsScreenHidesVisualizerTicks(t *testing.T) {
	m := Model{
		vis: ui.NewVisualizer(44100),
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

	m.vis.RequestRefresh()
	m.refreshVisualizerIfPending()

	if !m.vis.RefreshPending() {
		t.Fatal("refreshPending = false after lyrics-screen refresh attempt, want true")
	}
	if m.vis.Frame() != 0 {
		t.Fatalf("frame after lyrics-screen refresh attempt = %d, want 0", m.vis.Frame())
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
