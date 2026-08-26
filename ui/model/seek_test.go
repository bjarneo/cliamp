package model

import (
	"errors"
	"testing"
	"time"

	"github.com/bjarneo/cliamp/playlist"
	"github.com/bjarneo/cliamp/ui"
)

func TestSetSeekStepLarge(t *testing.T) {
	t.Run("sets positive value", func(t *testing.T) {
		m := Model{}
		m.SetSeekStepLarge(45 * time.Second)
		if got, want := m.seekStepLarge, 45*time.Second; got != want {
			t.Fatalf("seekStepLarge = %v, want %v", got, want)
		}
	})

	t.Run("resets non-positive to default", func(t *testing.T) {
		tests := []time.Duration{0, -5 * time.Second}
		for _, in := range tests {
			m := Model{}
			m.SetSeekStepLarge(in)
			if got, want := m.seekStepLarge, 30*time.Second; got != want {
				t.Fatalf("SetSeekStepLarge(%v): seekStepLarge = %v, want %v", in, got, want)
			}
		}
	})

	t.Run("clamps too-small positive value", func(t *testing.T) {
		m := Model{}
		m.SetSeekStepLarge(5 * time.Second)
		if got, want := m.seekStepLarge, 6*time.Second; got != want {
			t.Fatalf("seekStepLarge = %v, want %v", got, want)
		}
	})
}

// streamSeekModel returns a model playing a seekable stream track, the shape
// that routes seeks through the debounce path.
func streamSeekModel(eng *playbackFakeEngine) Model {
	m := Model{player: eng, playlist: playlist.New()}
	m.setPlaybackTrack(playlist.Track{Path: "https://nav/stream", Stream: true})
	return m
}

func TestStreamSeekDebounceSumsPresses(t *testing.T) {
	eng := &playbackFakeEngine{playing: true, seekable: true, duration: time.Hour}
	m := streamSeekModel(eng)

	for range 3 {
		if cmd := m.doSeek(5 * time.Second); cmd != nil {
			t.Fatal("doSeek returned a command, want the seek debounced")
		}
	}
	if len(eng.seekCalls) != 0 {
		t.Fatalf("Seek calls during debounce = %d, want 0", len(eng.seekCalls))
	}

	cmd := m.tickSeek(time.Duration(seekDebounceTicks) * ui.TickFast)
	if cmd == nil {
		t.Fatal("tickSeek returned nil, want the summed seek to fire")
	}
	if msg := cmd(); msg.(seekTickMsg).target != 15*time.Second {
		t.Fatalf("seek target = %v, want 15s", msg.(seekTickMsg).target)
	}
	if len(eng.seekCalls) != 1 || eng.seekCalls[0] != 15*time.Second {
		t.Fatalf("Seek calls = %v, want one 15s seek", eng.seekCalls)
	}
}

// A decoder restart can outlast the debounce window. The seek queued meanwhile
// must wait for the running one, or the two restarts race and the older target
// wins.
func TestStreamSeekWaitsForRunningSeek(t *testing.T) {
	eng := &playbackFakeEngine{playing: true, seekable: true, duration: time.Hour}
	m := streamSeekModel(eng)

	m.doSeek(10 * time.Second)
	first := m.tickSeek(time.Duration(seekDebounceTicks) * ui.TickFast)
	if first == nil {
		t.Fatal("tickSeek returned nil, want the first seek to fire")
	}

	// Second burst lands while the first restart is still running.
	m.doSeek(20 * time.Second)
	if cmd := m.tickSeek(time.Duration(seekDebounceTicks) * ui.TickFast); cmd != nil {
		t.Fatal("tickSeek returned a command, want the seek deferred until the first completes")
	}
	if len(eng.seekCalls) != 0 {
		t.Fatalf("Seek calls before the first seek runs = %v, want none", eng.seekCalls)
	}

	msg := first()
	eng.position = 10 * time.Second // the first restart landed
	updated, cmd := m.Update(msg)
	m = updated.(Model)
	if cmd == nil {
		t.Fatal("Update returned nil, want the deferred seek to fire")
	}
	cmd()

	if len(eng.seekCalls) != 2 {
		t.Fatalf("Seek calls = %v, want the first and the deferred seek", eng.seekCalls)
	}
	if got := eng.seekCalls[0] + eng.seekCalls[1]; got != 30*time.Second {
		t.Fatalf("final position = %v, want the latest target 30s", got)
	}
	if !m.seek.active {
		t.Fatal("seek.active = false, want the seek still marked active")
	}
}

// A seek that completes after the track changed says nothing about the new
// track, so its completion must not touch the new track's seek state.
func TestStaleSeekCompletionIgnored(t *testing.T) {
	eng := &playbackFakeEngine{playing: true, seekable: true, duration: time.Hour}
	m := streamSeekModel(eng)

	m.doSeek(10 * time.Second)
	stale := m.tickSeek(time.Duration(seekDebounceTicks) * ui.TickFast)
	if stale == nil {
		t.Fatal("tickSeek returned nil, want the seek to fire")
	}
	msg := stale()

	// The track changes while that seek is still running.
	m.beginPlaybackTrack(playlist.Track{Path: "https://nav/other", Stream: true})
	m.seek.active = true
	m.seek.targetPos = 42 * time.Second

	updated, cmd := m.Update(msg)
	m = updated.(Model)
	if cmd != nil {
		t.Fatal("Update returned a command for a stale completion, want none")
	}
	if !m.seek.active || m.seek.targetPos != 42*time.Second {
		t.Fatalf("new track seek state = active:%v target:%v, want it untouched", m.seek.active, m.seek.targetPos)
	}

	// The new track can still seek: the stale seek must not have left it blocked.
	if cmd := m.seekAbsolute(60 * time.Second); cmd == nil {
		t.Fatal("seekAbsolute returned nil, want the new track's seek to fire")
	}
}

// The resume marker rides on one seek only. If a newer target chains onto it,
// the marker must still be spent, or a later restart seeks back to it.
func TestChainedSeekConsumesResumeMarker(t *testing.T) {
	eng := &playbackFakeEngine{playing: true, seekable: true, duration: time.Hour}
	m := streamSeekModel(eng)
	m.resume.path = "https://nav/stream"
	m.resume.secs = 300

	resumeSeek := m.seekCmd(300*time.Second, true)
	m.seek.active = true
	m.seek.inFlight = true

	// A newer seek arrives while the resume seek is running.
	m.doSeek(30 * time.Second)
	m.tickSeek(time.Duration(seekDebounceTicks) * ui.TickFast)
	if !m.seek.pending {
		t.Fatal("seek.pending = false, want the newer target queued")
	}

	updated, cmd := m.Update(resumeSeek())
	m = updated.(Model)
	if cmd == nil {
		t.Fatal("Update returned nil, want the chained seek to fire")
	}
	if m.resume.path != "" || m.resume.secs != 0 {
		t.Fatalf("resume state = %q/%ds, want it consumed", m.resume.path, m.resume.secs)
	}
}

// A failed seek must not swallow the target the user queued behind it.
func TestPendingSeekSurvivesFailedSeek(t *testing.T) {
	eng := &playbackFakeEngine{playing: true, seekable: true, duration: time.Hour}
	m := streamSeekModel(eng)

	m.doSeek(10 * time.Second)
	first := m.tickSeek(time.Duration(seekDebounceTicks) * ui.TickFast)
	if first == nil {
		t.Fatal("tickSeek returned nil, want the first seek to fire")
	}
	first()

	m.doSeek(20 * time.Second)
	m.tickSeek(time.Duration(seekDebounceTicks) * ui.TickFast)
	if !m.seek.pending {
		t.Fatal("seek.pending = false, want the newer target queued")
	}

	failed := seekTickMsg{err: errors.New("ffmpeg restart failed"), target: 10 * time.Second, gen: m.seek.gen}
	updated, cmd := m.Update(failed)
	m = updated.(Model)
	if cmd == nil {
		t.Fatal("Update returned nil after a failed seek, want the queued target to fire")
	}
	cmd()
	if len(eng.seekCalls) != 2 {
		t.Fatalf("Seek calls = %v, want the failed seek and the queued one", eng.seekCalls)
	}
}
