package model

import (
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/bjarneo/cliamp/internal/playback"
	"github.com/bjarneo/cliamp/playlist"
)

// Lua plugins change the volume, the speed and mono through messages. The
// Update loop applies each change, tells the media controls about the volume
// and saves the speed after the debounce, as the keys do.
func TestPluginPlayerMessages(t *testing.T) {
	tests := []struct {
		name  string
		msg   tea.Msg
		check func(t *testing.T, m Model, engine *playbackFakeEngine, notifier *fakeNotifier)
	}{
		{
			name: "volume",
			msg:  playback.SetVolumeMsg{VolumeDB: -40},
			check: func(t *testing.T, _ Model, engine *playbackFakeEngine, notifier *fakeNotifier) {
				if engine.volume != -40 {
					t.Errorf("volume = %v, want -40", engine.volume)
				}
				if n := len(notifier.updates); n == 0 || notifier.updates[n-1].VolumeDB != -40 {
					t.Errorf("notifier updates = %+v, want the new volume", notifier.updates)
				}
			},
		},
		{
			name: "speed",
			msg:  playback.SetSpeedMsg{Ratio: 1.5},
			check: func(t *testing.T, m Model, engine *playbackFakeEngine, _ *fakeNotifier) {
				if got := engine.Speed(); got != 1.5 {
					t.Errorf("speed = %v, want 1.5", got)
				}
				if m.speedSaveAfter != speedSaveDebounce {
					t.Errorf("speedSaveAfter = %v, want %v", m.speedSaveAfter, speedSaveDebounce)
				}
			},
		},
		{
			name: "mono",
			msg:  playback.ToggleMonoMsg{},
			check: func(t *testing.T, _ Model, engine *playbackFakeEngine, _ *fakeNotifier) {
				if !engine.mono {
					t.Error("mono = false, want true")
				}
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			engine := &playbackFakeEngine{}
			notifier := &fakeNotifier{}
			m := Model{player: engine, playlist: playlist.New(), configSaver: &recordingSaver{}, notifier: notifier}
			updated, _ := m.Update(tt.msg)
			tt.check(t, updated.(Model), engine, notifier)
		})
	}
}
