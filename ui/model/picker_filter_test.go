package model

import (
	"strings"
	"testing"

	"github.com/bjarneo/cliamp/theme"
	"github.com/bjarneo/cliamp/ui"
	"github.com/charmbracelet/x/ansi"
)

func TestThemePickerFilterPreservesRawThemeIndex(t *testing.T) {
	m := Model{
		themes: []theme.Theme{
			{Name: "Ayu", Accent: "#000000", BrightFG: "#ffffff", FG: "#111111", Green: "#00ff00", Yellow: "#ffff00", Red: "#ff0000"},
			{Name: "Midnight", Accent: "#000000", BrightFG: "#ffffff", FG: "#111111", Green: "#00ff00", Yellow: "#ffff00", Red: "#ff0000"},
		},
		themePicker: themePickerState{filter: "mid"},
	}
	m.themePickerRecomputeFilter()

	if len(m.themePicker.filtered) != 1 || m.themePicker.filtered[0] != 2 {
		t.Fatalf("filtered theme indices = %v, want [2]", m.themePicker.filtered)
	}
	if rawIdx, ok := m.themePickerRawIndex(0); !ok || rawIdx != 2 {
		t.Fatalf("raw theme index = %d, %t; want 2, true", rawIdx, ok)
	}
}

func TestThemePickerCancelHandlesRemovedTheme(t *testing.T) {
	m := Model{themePicker: themePickerState{visible: true, savedName: "Removed"}}
	m.themePickerCancel()
	if m.themePicker.visible {
		t.Fatal("theme picker remains visible after cancel")
	}
	if m.themeIdx != -1 {
		t.Fatalf("theme index = %d, want default index -1", m.themeIdx)
	}
}

func TestThemePickerSelectPersistsSelectedTheme(t *testing.T) {
	saver := &recordingConfigSaver{}
	m := Model{
		themes: []theme.Theme{
			{Name: "Ayu", Accent: "#000000", BrightFG: "#ffffff", FG: "#111111", Green: "#00ff00", Yellow: "#ffff00", Red: "#ff0000"},
		},
		themePicker: themePickerState{visible: true, cursor: 1},
		configSaver: saver,
	}

	m.themePickerSelect()

	if got := saver.values["theme"]; got != `"Ayu"` {
		t.Fatalf("saved theme = %q, want %q", got, `"Ayu"`)
	}
	if m.themePicker.visible {
		t.Fatal("theme picker remains visible after selection")
	}
}

func TestThemePickerSelectPersistsDefaultAsEmptyValue(t *testing.T) {
	saver := &recordingConfigSaver{}
	m := Model{
		themes:      []theme.Theme{{Name: "Ayu"}},
		themePicker: themePickerState{visible: true, cursor: 0},
		configSaver: saver,
	}

	m.themePickerSelect()

	if got := saver.values["theme"]; got != `""` {
		t.Fatalf("saved default theme = %q, want %q", got, `""`)
	}
}

func TestVisualizerPickerFilterPreservesModeIndex(t *testing.T) {
	m := Model{
		vis: ui.NewVisualizer(44_100),
		visPicker: visPickerState{
			modes:  []string{"None", "Bars", "Wave"},
			filter: "wa",
		},
	}
	m.visPickerRecomputeFilter()

	if len(m.visPicker.filtered) != 1 || m.visPicker.filtered[0] != 2 {
		t.Fatalf("filtered visualizer indices = %v, want [2]", m.visPicker.filtered)
	}
	if rawIdx, ok := m.visPickerRawIndex(0); !ok || rawIdx != 2 {
		t.Fatalf("raw visualizer index = %d, %t; want 2, true", rawIdx, ok)
	}
}

func TestPickerFilterHelpDescribesFilterInput(t *testing.T) {
	m := Model{themePicker: themePickerState{filtering: true}}
	plain := ansi.Strip(m.themePickerHelpLine())
	if !strings.Contains(plain, "Cancel filter") || !strings.Contains(plain, "Finish filter") {
		t.Fatalf("theme filter help = %q, want filter actions", plain)
	}
}
