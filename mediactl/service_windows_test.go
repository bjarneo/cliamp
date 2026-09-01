//go:build windows

package mediactl

import (
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/bjarneo/cliamp/internal/playback"
)

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

func TestServiceUpdateAndSeekedAreNoOps(t *testing.T) {
	svc, err := New(func(tea.Msg) { t.Fatal("send() should not be called by Update/Seeked") })
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer svc.Close()

	svc.Update(playback.State{Status: playback.StatusPlaying})
	svc.Seeked(0)
}

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
