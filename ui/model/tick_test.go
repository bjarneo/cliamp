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

// TestTickIntervalStoppedUsesSlow verifies that when the player is stopped,
// the tick interval is ui.TickSlow (~200ms) not ui.TickFast (~50ms), regardless of
// the visualizer mode. This matters for CPU usage (issue #92).
func TestTickIntervalStoppedUsesSlow(t *testing.T) {
	if sharedPlayer == nil {
		t.Skip("audio hardware unavailable")
	}
	m := Model{
		player:    sharedPlayer,
		vis:       ui.NewVisualizer(float64(sharedPlayer.SampleRate())),
		playlist:  playlist.New(),
		termTitle: terminalTitleState{},
	}

	// Player is stopped by default (IsPlaying=false).
	// vis.Mode defaults to ui.VisBars (0 != ui.VisNone).
	if sharedPlayer.IsPlaying() {
		t.Fatal("expected player to be stopped")
	}
	if m.vis.Mode == ui.VisNone {
		t.Fatal("expected default vis mode to be non-None (ui.VisBars)")
	}

	_, cmd := m.Update(tickMsg(time.Now()))
	if cmd == nil {
		t.Fatal("tickMsg returned nil cmd")
	}

	start := time.Now()
	cmd() // blocks until the tick timer fires
	elapsed := time.Since(start)

	// ui.TickSlow=200ms, ui.TickFast=50ms. With tolerance for scheduling jitter.
	const tolerance = 80 * time.Millisecond
	if elapsed < ui.TickSlow-tolerance {
		t.Errorf("tick fired after %v, want ~%v (ui.TickSlow); got ui.TickFast instead — CPU fix not working",
			elapsed, ui.TickSlow)
	}
	t.Logf("tick interval when stopped: %v (want ~%v ui.TickSlow)", elapsed.Round(time.Millisecond), ui.TickSlow)
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
