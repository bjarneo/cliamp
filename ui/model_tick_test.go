package ui

import (
	"os"
	"testing"
	"time"

	"cliamp/player"
	"cliamp/playlist"
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
		player:   sharedPlayer,
		vis:      NewVisualizer(float64(sharedPlayer.SampleRate())),
		playlist: playlist.New(),
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
