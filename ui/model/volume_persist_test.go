package model

import (
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/bjarneo/cliamp/internal/playback"
	"github.com/bjarneo/cliamp/ui"
)

func newVolumeTestModel() (Model, *settingsFocusEngine, *fakeNotifier, *recordingConfigSaver) {
	m := newColumnTestModel(100, 30)
	p := &settingsFocusEngine{}
	notifier := &fakeNotifier{}
	saver := &recordingConfigSaver{}
	m.player, m.notifier, m.configSaver = p, notifier, saver
	m.plCursor = 2
	return m, p, notifier, saver
}

func TestVolumeKeySchedulesDebouncedSave(t *testing.T) {
	tests := []struct {
		name  string
		focus focusArea
		key   tea.KeyPressMsg
		want  float64
	}{
		{name: "plus", focus: focusPlaylist, key: tea.KeyPressMsg{Text: "+"}, want: 1},
		{name: "equals", focus: focusPlaylist, key: tea.KeyPressMsg{Text: "="}, want: 1},
		{name: "minus", focus: focusPlaylist, key: tea.KeyPressMsg{Text: "-"}, want: -1},
		{name: "right", focus: focusVolume, key: tea.KeyPressMsg{Code: tea.KeyRight}, want: 1},
		{name: "left", focus: focusVolume, key: tea.KeyPressMsg{Code: tea.KeyLeft}, want: -1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m, p, notifier, saver := newVolumeTestModel()
			m.focus = tt.focus

			m.handleKey(tt.key)

			if p.volume != tt.want {
				t.Fatalf("volume = %v, want %v", p.volume, tt.want)
			}
			if len(notifier.updates) != 1 || notifier.updates[0].VolumeDB != tt.want {
				t.Fatalf("notifications = %v, want one update with VolumeDB %v", notifier.updates, tt.want)
			}
			if got := m.volumeSaveAfter; got != volumeSaveDebounce {
				t.Fatalf("volumeSaveAfter = %v, want %v", got, volumeSaveDebounce)
			}
			if len(saver.values) != 0 {
				t.Fatalf("saver values = %v, want no immediate save", saver.values)
			}
		})
	}
}

func TestTickPendingVolumeSaveDebounces(t *testing.T) {
	m, _, _, saver := newVolumeTestModel()
	m.handleKey(tea.KeyPressMsg{Text: "+"})

	for i := range 4 {
		m.tickPendingVolumeSave(ui.TickSlow)
		if len(saver.values) != 0 {
			t.Fatalf("saved after %d slow ticks, want no save before %v", i+1, volumeSaveDebounce)
		}
	}

	m.tickPendingVolumeSave(ui.TickSlow)

	if got := saver.values["volume"]; got != "1" {
		t.Fatalf("saved volume = %q, want %q", got, "1")
	}
	if got := m.volumeSaveAfter; got != 0 {
		t.Fatalf("volumeSaveAfter after save = %v, want 0", got)
	}
}

func TestFlushPendingVolumeSavePersistsImmediately(t *testing.T) {
	m, _, _, saver := newVolumeTestModel()
	m.handleKey(tea.KeyPressMsg{Text: "-"})
	m.flushPendingVolumeSave()

	if got := saver.values["volume"]; got != "-1" {
		t.Fatalf("saved volume = %q, want %q", got, "-1")
	}
	if got := m.volumeSaveAfter; got != 0 {
		t.Fatalf("volumeSaveAfter after flush = %v, want 0", got)
	}
}

func TestSetVolumeMsgSchedulesVolumeSave(t *testing.T) {
	m, p, _, saver := newVolumeTestModel()

	next, _ := m.Update(playback.SetVolumeMsg{VolumeDB: -7})
	got := next.(Model)

	if p.volume != -7 {
		t.Fatalf("volume = %v, want -7", p.volume)
	}
	if got.volumeSaveAfter != volumeSaveDebounce {
		t.Fatalf("volumeSaveAfter = %v, want %v", got.volumeSaveAfter, volumeSaveDebounce)
	}
	if len(saver.values) != 0 {
		t.Fatalf("saver values = %v, want no immediate save", saver.values)
	}
}
