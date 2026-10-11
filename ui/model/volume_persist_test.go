package model

import (
	"errors"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/bjarneo/cliamp/internal/playback"
	"github.com/bjarneo/cliamp/ui"
)

func newVolumeTestModel() (Model, *playbackFakeEngine, *fakeNotifier, *recordingSaver) {
	m := newColumnTestModel(100, 30)
	p := &playbackFakeEngine{}
	notifier := &fakeNotifier{}
	saver := &recordingSaver{}
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

			next, _ := m.Update(tt.key)
			m = next.(Model)

			if p.volume != tt.want {
				t.Fatalf("volume = %v, want %v", p.volume, tt.want)
			}
			if len(notifier.updates) != 1 || notifier.updates[0].VolumeDB != tt.want {
				t.Fatalf("notifications = %v, want one update with VolumeDB %v", notifier.updates, tt.want)
			}
			if got := m.volumeSaveAfter; got != volumeSaveDebounce {
				t.Fatalf("volumeSaveAfter = %v, want %v", got, volumeSaveDebounce)
			}
			if len(saver.saved) != 0 {
				t.Fatalf("saver saved = %v, want no immediate save", saver.saved)
			}
		})
	}
}

func TestTickPendingVolumeSaveDebounces(t *testing.T) {
	m, _, _, saver := newVolumeTestModel()
	next, _ := m.Update(tea.KeyPressMsg{Text: "+"})
	m = next.(Model)

	for i := range 4 {
		m.tickPendingVolumeSave(ui.TickSlow)
		if len(saver.saved) != 0 {
			t.Fatalf("saved after %d slow ticks, want no save before %v", i+1, volumeSaveDebounce)
		}
	}

	m.tickPendingVolumeSave(ui.TickSlow)

	if got := saver.saved["volume"]; got != "1" {
		t.Fatalf("saved volume = %q, want %q", got, "1")
	}
	if got := m.volumeSaveAfter; got != 0 {
		t.Fatalf("volumeSaveAfter after save = %v, want 0", got)
	}
}

func TestFlushPendingVolumeSavePersistsImmediately(t *testing.T) {
	m, _, _, saver := newVolumeTestModel()
	next, _ := m.Update(tea.KeyPressMsg{Text: "-"})
	m = next.(Model)
	m.flushPendingVolumeSave()

	if got := saver.saved["volume"]; got != "-1" {
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
	if len(saver.saved) != 0 {
		t.Fatalf("saver saved = %v, want no immediate save", saver.saved)
	}
}

// failNTimesSaver fails the first failLeft Save calls, then delegates to
// the recording saver.
type failNTimesSaver struct {
	recordingSaver
	failLeft int
}

func (s *failNTimesSaver) SaveFloat(key string, value float64, prec int) error {
	if s.failLeft > 0 {
		s.failLeft--
		return errors.New("disk full")
	}
	return s.recordingSaver.SaveFloat(key, value, prec)
}

func TestTickPendingVolumeSaveRetriesAfterFailure(t *testing.T) {
	m := newColumnTestModel(100, 30)
	p := &playbackFakeEngine{}
	notifier := &fakeNotifier{}
	saver := &failNTimesSaver{failLeft: 1}
	m.player, m.notifier, m.configSaver = p, notifier, saver
	m.plCursor = 2
	next, _ := m.Update(tea.KeyPressMsg{Text: "+"})
	m = next.(Model)

	for range 5 {
		m.tickPendingVolumeSave(ui.TickSlow)
	}
	if len(saver.saved) != 0 {
		t.Fatalf("saver saved = %v, want no save after failed write", saver.saved)
	}
	if got := m.volumeSaveAfter; got != volumeSaveDebounce {
		t.Fatalf("volumeSaveAfter after failed save = %v, want re-armed %v", got, volumeSaveDebounce)
	}

	for range 5 {
		m.tickPendingVolumeSave(ui.TickSlow)
	}
	if got := saver.saved["volume"]; got != "1" {
		t.Fatalf("saved volume = %q, want %q", got, "1")
	}
	if got := m.volumeSaveAfter; got != 0 {
		t.Fatalf("volumeSaveAfter after retry save = %v, want 0", got)
	}
}

func TestVolumePersistenceThroughUpdate(t *testing.T) {
	t.Run("tick", func(t *testing.T) {
		m, _, _, saver := newVolumeTestModel()
		next, _ := m.Update(tea.KeyPressMsg{Text: "+"})
		m = next.(Model)
		m.volumeSaveAfter = time.Millisecond

		next, _ = m.Update(tickMsg(time.Now()))
		got := next.(Model)

		if v := saver.saved["volume"]; v != "1" {
			t.Fatalf("saved volume = %q, want %q", v, "1")
		}
		if got.volumeSaveAfter != 0 {
			t.Fatalf("volumeSaveAfter = %v, want 0", got.volumeSaveAfter)
		}
	})

	t.Run("quit", func(t *testing.T) {
		m, _, _, saver := newVolumeTestModel()
		next, _ := m.Update(tea.KeyPressMsg{Text: "-"})
		m = next.(Model)

		_, cmd := m.Update(playback.QuitMsg{})

		if v := saver.saved["volume"]; v != "-1" {
			t.Fatalf("saved volume = %q, want %q", v, "-1")
		}
		if cmd == nil {
			t.Fatal("Update(QuitMsg) cmd = nil, want tea.Quit")
		}
	})
}
