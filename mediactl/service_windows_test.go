//go:build windows

package mediactl

import (
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/bjarneo/cliamp/internal/playback"
)

// TestHotkeyMsg checks that each hotkey ID, including its Win-held twin,
// maps to the right playback message, and that an unknown ID is rejected.
func TestHotkeyMsg(t *testing.T) {
	tests := []struct {
		name string
		id   int32
		want tea.Msg
		ok   bool
	}{
		{"play/pause", hotkeyIDPlayPause, playback.PlayPauseMsg{}, true},
		{"next", hotkeyIDNext, playback.NextMsg{}, true},
		{"previous", hotkeyIDPrev, playback.PrevMsg{}, true},
		{"stop", hotkeyIDStop, playback.StopMsg{}, true},
		{"play/pause win-held", hotkeyIDPlayPauseWin, playback.PlayPauseMsg{}, true},
		{"next win-held", hotkeyIDNextWin, playback.NextMsg{}, true},
		{"previous win-held", hotkeyIDPrevWin, playback.PrevMsg{}, true},
		{"stop win-held", hotkeyIDStopWin, playback.StopMsg{}, true},
		{"unknown id", 99, nil, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := hotkeyMsg(tt.id)
			if ok != tt.ok {
				t.Fatalf("hotkeyMsg(%d) ok = %v, want %v", tt.id, ok, tt.ok)
			}
			if ok && got != tt.want {
				t.Fatalf("hotkeyMsg(%d) = %#v, want %#v", tt.id, got, tt.want)
			}
		})
	}
}

// TestHotkeyModifiers checks that the Win-held hotkey IDs register with
// MOD_WIN added, and every other ID stays modNoRepeat-only.
func TestHotkeyModifiers(t *testing.T) {
	winHeldIDs := map[int]bool{
		hotkeyIDPlayPauseWin: true,
		hotkeyIDNextWin:      true,
		hotkeyIDPrevWin:      true,
		hotkeyIDStopWin:      true,
	}

	for id := range hotkeyVKs {
		got := hotkeyModifiers(id)
		want := uint32(modNoRepeat)
		if winHeldIDs[id] {
			want |= modWin
		}
		if got != want {
			t.Errorf("hotkeyModifiers(%d) = %#x, want %#x", id, got, want)
		}
	}
}

// TestServiceUpdateAndSeekedAreNoOps checks that Update and Seeked never
// send a playback message, since Windows has no now-playing metadata to report.
func TestServiceUpdateAndSeekedAreNoOps(t *testing.T) {
	svc, err := New(func(tea.Msg) { t.Fatal("send() should not be called by Update/Seeked") })
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer svc.Close()

	svc.Update(playback.State{Status: playback.StatusPlaying})
	svc.Seeked(0)
}

// TestNewRegistersHotkeysBeforeReturning checks that New doesn't return
// until the message loop has registered its hotkeys and reported its thread ID.
func TestNewRegistersHotkeysBeforeReturning(t *testing.T) {
	svc, err := New(func(tea.Msg) {})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer svc.Close()

	if svc.threadID == 0 {
		t.Fatal("New() returned before the message loop reported a thread id")
	}
}

// TestServiceCloseStopsMessageLoopAndIsIdempotent checks that Close stops
// the message loop goroutine and is safe to call a second time.
func TestServiceCloseStopsMessageLoopAndIsIdempotent(t *testing.T) {
	svc, err := New(func(tea.Msg) {})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	svc.Close()

	select {
	case <-svc.stopped:
	default:
		t.Fatal("Close() returned before the message loop goroutine stopped")
	}

	svc.Close() // must not block or panic on a second call
}
