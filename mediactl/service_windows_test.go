//go:build windows

package mediactl

import (
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/bjarneo/cliamp/internal/playback"
)

// A Win-held ID exists only so RegisterHotKey matches when Win happens to
// be down; it must still collapse to the exact same playback message as
// its plain counterpart, or the two registrations would diverge in effect.
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

// Guards against a future hotkey ID being added to hotkeyVKs without a
// matching case here: it would silently fall through to modNoRepeat only,
// the same bug this file exists to fix.
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

// The headless daemon never calls Run, so New is the only point where it
// can be sure the hotkeys are live; a caller racing ahead of registration
// would have a window where media keys silently do nothing.
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

// Close can end up called from more than one shutdown path (an explicit
// close alongside a deferred one, say), so a second call must not panic
// or block waiting on a message loop that already exited.
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
