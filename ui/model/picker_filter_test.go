package model

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

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
		themePicker: themePickerState{filterList: filterList{filter: "mid"}},
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
	saver := &recordingSaver{}
	m := Model{
		themes: []theme.Theme{
			{Name: "Ayu", Accent: "#000000", BrightFG: "#ffffff", FG: "#111111", Green: "#00ff00", Yellow: "#ffff00", Red: "#ff0000"},
		},
		themePicker: themePickerState{visible: true, filterList: filterList{cursor: 1}},
		configSaver: saver,
	}

	m.themePickerSelect()

	if got := saver.saved["theme"]; got != `"Ayu"` {
		t.Fatalf("saved theme = %q, want %q", got, `"Ayu"`)
	}
	if m.themePicker.visible {
		t.Fatal("theme picker remains visible after selection")
	}
}

func TestThemePickerSelectPersistsDefaultAsEmptyValue(t *testing.T) {
	saver := &recordingSaver{}
	m := Model{
		themes:      []theme.Theme{{Name: "Ayu"}},
		themePicker: themePickerState{visible: true},
		configSaver: saver,
	}

	m.themePickerSelect()

	if got := saver.saved["theme"]; got != `""` {
		t.Fatalf("saved default theme = %q, want %q", got, `""`)
	}
}

func TestVisualizerPickerFilterPreservesModeIndex(t *testing.T) {
	m := Model{
		vis: ui.NewVisualizer(44_100),
		visPicker: visPickerState{
			modes:      []string{"None", "Bars", "Wave"},
			filterList: filterList{filter: "wa"},
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
	m := Model{themePicker: themePickerState{visible: true, filterList: filterList{filtering: true}}}
	plain := ansi.Strip(m.renderHelp())
	if !strings.Contains(plain, "Cancel filter") || !strings.Contains(plain, "Finish filter") {
		t.Fatalf("theme filter help = %q, want filter actions", plain)
	}
}

// TestPickerListsWindowTheShownRows checks that the theme and visualizer
// pickers draw the rows the filter shows, from the scroll offset, with the
// cursor on the view row. The theme picker draws theme.DefaultName in row 0,
// so the test reads it as None. The filters do not match that name.
func TestPickerListsWindowTheShownRows(t *testing.T) {
	tests := []struct {
		name      string
		filtering bool
		filter    string
		cursor    int
		scroll    int
		want      []string
	}{
		{name: "all rows", cursor: 1, want: []string{"  None", "> Bars"}},
		{name: "scrolled", cursor: 3, scroll: 2, want: []string{"  Bricks", "> Scope"}},
		{name: "empty filter field", filtering: true, cursor: 1, want: []string{"  None", "> Bars"}},
		{name: "filtered", filter: "b", cursor: 1, want: []string{"  Bars", "> Bricks"}},
		{name: "no match", filter: "zzz", want: []string{"  No matches.", ""}},
	}
	names := []string{"None", "Bars", "Bricks", "Scope"}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			list := filterList{filtering: tt.filtering, filter: tt.filter}

			vis := Model{vis: ui.NewVisualizer(44_100), plVisible: 2}
			vis.visPicker = visPickerState{modes: names, filterList: list}
			vis.visPickerRecomputeFilter()
			vis.visPicker.cursor, vis.visPicker.scroll = tt.cursor, tt.scroll

			themes := Model{plVisible: 2, themePicker: themePickerState{filterList: list}}
			for _, name := range names[1:] {
				themes.themes = append(themes.themes, theme.Theme{Name: name})
			}
			themes.themePickerRecomputeFilter()
			themes.themePicker.cursor, themes.themePicker.scroll = tt.cursor, tt.scroll

			for picker, body := range map[string]string{"visualizer": vis.renderVisPickerList(), "theme": themes.renderThemeBody()} {
				got := strings.Split(strings.ReplaceAll(ansi.Strip(body), theme.DefaultName, "None"), "\n")
				if strings.Join(got, "|") != strings.Join(tt.want, "|") {
					t.Errorf("%s picker rows = %q, want %q", picker, got, tt.want)
				}
			}
		})
	}
}

// TestVisPickerKeepsCursorRowAcrossVisNone checks that a cursor key which moves
// onto or off VisNone fits the picker window to the playlist rows after the
// layout change, so the window shows the cursor row and no blank rows.
func TestVisPickerKeepsCursorRowAcrossVisNone(t *testing.T) {
	tests := []struct {
		name  string
		start ui.VisMode
		key   tea.KeyPressMsg
	}{
		{name: "up from None", start: ui.VisNone, key: tea.KeyPressMsg{Code: 'k', Text: "k"}},
		{name: "end from Bars", start: ui.VisBars, key: tea.KeyPressMsg{Code: 'G', Text: "G"}},
		{name: "down onto None", start: ui.VisNone - 1, key: tea.KeyPressMsg{Code: 'j', Text: "j"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := newLayoutTestModel(80, 24)
			m.vis.Mode = tt.start
			m.recomputeLayout()
			m.openVisPicker()

			updated, _ := m.Update(tt.key)
			m = updated.(Model)

			rows := strings.Split(ansi.Strip(m.renderVisPickerList()), "\n")
			var cursorRow bool
			for i, row := range rows {
				if strings.TrimSpace(row) == "" {
					t.Errorf("row %d is blank, rows = %q", i, rows)
				}
				cursorRow = cursorRow || strings.HasPrefix(row, "> ")
			}
			if !cursorRow {
				t.Errorf("no cursor row in %q", rows)
			}
		})
	}
}
