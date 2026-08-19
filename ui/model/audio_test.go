package model

import (
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/bjarneo/cliamp/playlist"
)

type recordingConfigSaver struct {
	values map[string]string
}

func (s *recordingConfigSaver) Save(key, value string) error {
	if s.values == nil {
		s.values = make(map[string]string)
	}
	s.values[key] = value
	return nil
}

func newEQTestModel(custom [eqBandCount]float64, saver ConfigSaver) Model {
	player := &playbackFakeEngine{eqBands: custom}
	m := New(player, playlist.New(), nil, "", nil, nil, nil, saver)
	m.layout.tier = layoutFull
	m.focus = focusEQ
	return m
}

func TestCycleEQPresetReturnsToCustomCurve(t *testing.T) {
	custom := [eqBandCount]float64{6, 4, 2, 0, -2, 1, 3, 5, 4, 2}
	m := newEQTestModel(custom, &recordingConfigSaver{})

	for range len(eqPresets) + 1 {
		m.handleKey(tea.KeyPressMsg{Text: "e"})
	}

	if got := m.EQPresetName(); got != "Custom" {
		t.Fatalf("EQ preset = %q, want Custom", got)
	}
	if got := m.player.EQBands(); got != custom {
		t.Fatalf("EQ bands = %v, want custom curve %v", got, custom)
	}
}

func TestBuiltInPresetSavePreservesCustomCurve(t *testing.T) {
	custom := [eqBandCount]float64{6, 4, 2, 0, -2, 1, 3, 5, 4, 2}
	saver := &recordingConfigSaver{}
	m := newEQTestModel(custom, saver)

	m.handleKey(tea.KeyPressMsg{Text: "e"})
	m.saveEQ()

	if got := saver.values["eq_preset"]; got != `"Flat"` {
		t.Fatalf("saved eq_preset = %q, want %q", got, `"Flat"`)
	}
	if got := saver.values["eq"]; got != "[6, 4, 2, 0, -2, 1, 3, 5, 4, 2]" {
		t.Fatalf("saved eq = %q, want custom curve %v", got, custom)
	}
}

func TestLoadedCustomCurveSurvivesActiveBuiltInPreset(t *testing.T) {
	custom := [eqBandCount]float64{6, 4, 2, 0, -2, 1, 3, 5, 4, 2}
	m := newEQTestModel(eqPresets[0].Bands, &recordingConfigSaver{})
	m.SetCustomEQBands(custom)
	m.SetEQPreset("Flat", nil)

	m.SetEQPreset("Custom", nil)

	if got := m.player.EQBands(); got != custom {
		t.Fatalf("EQ bands = %v, want loaded custom curve %v", got, custom)
	}
}

func TestBandChangeReplacesCustomCurve(t *testing.T) {
	m := newEQTestModel([eqBandCount]float64{}, &recordingConfigSaver{})
	m.SetEQPreset("Flat", nil)
	want := eqPresets[0].Bands
	want[2] = 5

	m.setCustomEQBand(2, 5)
	m.SetEQPreset("Rock", nil)
	m.SetEQPreset("Custom", nil)

	if got := m.player.EQBands(); got != want {
		t.Fatalf("EQ bands = %v, want updated custom curve %v", got, want)
	}
}
