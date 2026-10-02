package model

import (
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/bjarneo/cliamp/theme"
	"github.com/bjarneo/cliamp/ui"
)

func TestFilterListViewCountAndRawIndex(t *testing.T) {
	tests := []struct {
		name      string
		list      filterList
		n         int
		wantCount int
		view      int
		wantRaw   int
		wantOK    bool
	}{
		{name: "unfiltered shows every row", list: filterList{}, n: 4, wantCount: 4, view: 3, wantRaw: 3, wantOK: true},
		{name: "unfiltered row past the end", list: filterList{}, n: 4, wantCount: 4, view: 4},
		{name: "filter maps the view", list: filterList{filter: "a", filtered: []int{1, 3}}, n: 4, wantCount: 2, view: 1, wantRaw: 3, wantOK: true},
		{name: "empty filter field shows its view", list: filterList{filtering: true, filtered: []int{0, 2}}, n: 4, wantCount: 2, view: 1, wantRaw: 2, wantOK: true},
		{name: "filter with no match", list: filterList{filter: "zzz"}, n: 4, wantCount: 0, view: 0},
		{name: "negative view row", list: filterList{filter: "a", filtered: []int{1}}, n: 4, wantCount: 1, view: -1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.list.viewCount(tt.n); got != tt.wantCount {
				t.Errorf("viewCount(%d) = %d, want %d", tt.n, got, tt.wantCount)
			}
			raw, ok := tt.list.rawIndex(tt.view, tt.n)
			if ok != tt.wantOK || (ok && raw != tt.wantRaw) {
				t.Errorf("rawIndex(%d, %d) = %d, %t; want %d, %t", tt.view, tt.n, raw, ok, tt.wantRaw, tt.wantOK)
			}
			items := make([]int, tt.n)
			for i := range items {
				items[i] = i
			}
			rows := shownRows(&tt.list, items)
			if len(rows) != tt.wantCount {
				t.Errorf("shownRows = %v, want %d rows", rows, tt.wantCount)
			}
			for view, row := range rows {
				if raw, ok := tt.list.rawIndex(view, tt.n); !ok || raw != row {
					t.Errorf("shownRows[%d] = %d, rawIndex = %d, %t", view, row, raw, ok)
				}
			}
		})
	}
}

func TestFilterListRecomputeKeepsOrderAndResetsCursor(t *testing.T) {
	l := filterList{cursor: 3, scroll: 2, filter: "x"}
	l.recompute(5, func(raw int) bool { return raw%2 == 1 })
	if len(l.filtered) != 2 || l.filtered[0] != 1 || l.filtered[1] != 3 {
		t.Fatalf("filtered = %v, want [1 3]", l.filtered)
	}
	if l.cursor != 0 || l.scroll != 0 {
		t.Fatalf("cursor, scroll = %d, %d; want 0, 0", l.cursor, l.scroll)
	}
}

// filterOverlay opens one overlay that embeds filterList, with 3 rows and the
// cursor on the last row.
type filterOverlay struct {
	name string
	open func() Model
	key  func(*Model, tea.KeyPressMsg) tea.Cmd
	list func(*Model) *filterList
}

func filterOverlays() []filterOverlay {
	return []filterOverlay{
		{
			name: "theme picker",
			open: func() Model {
				m := Model{themes: []theme.Theme{{Name: "Ayu"}, {Name: "Midnight"}}}
				m.themePicker.visible = true
				m.themePicker.cursor = 2
				return m
			},
			key:  (*Model).handleThemeKey,
			list: func(m *Model) *filterList { return &m.themePicker.filterList },
		},
		{
			name: "visualizer picker",
			open: func() Model {
				m := Model{vis: ui.NewVisualizer(44_100)}
				m.visPicker.visible = true
				m.visPicker.modes = []string{"Bars", "Wave", "Scope"}
				m.visPicker.cursor = 2
				return m
			},
			key:  (*Model).handleVisPickerKey,
			list: func(m *Model) *filterList { return &m.visPicker.filterList },
		},
		{
			name: "keymap",
			open: func() Model {
				m := Model{}
				m.keymap.visible = true
				m.keymap.entries = []keymapEntry{{key: "a", action: "One"}, {key: "b", action: "Two"}, {key: "c", action: "Three"}}
				m.keymap.cursor = 2
				return m
			},
			key:  (*Model).handleKeymapKey,
			list: func(m *Model) *filterList { return &m.keymap.filterList },
		},
		{
			name: "file browser",
			open: func() Model {
				m := Model{}
				m.fileBrowser.visible = true
				m.fileBrowser.selected = map[string]bool{}
				m.fileBrowser.entries = []fbEntry{{name: "..", isDir: true, isParent: true}, {name: "a.mp3", isAudio: true}, {name: "b.mp3", isAudio: true}}
				m.fileBrowser.cursor = 2
				return m
			},
			key:  (*Model).handleFileBrowserKey,
			list: func(m *Model) *filterList { return &m.fileBrowser.filterList },
		},
	}
}

func TestFilterKeysMatchAcrossOverlays(t *testing.T) {
	press := func(key string) tea.KeyPressMsg {
		msg, ok := keyPressFor(key)
		if !ok {
			t.Fatalf("keyPressFor(%q) failed", key)
		}
		return msg
	}
	tests := []struct {
		name          string
		keys          []tea.KeyPressMsg
		wantFiltering bool
		wantFilter    string
		wantCursor    int
	}{
		{name: "esc restores the place", keys: []tea.KeyPressMsg{press("/"), press("z"), press("esc")}, wantCursor: 2},
		{name: "enter on an empty filter restores the place", keys: []tea.KeyPressMsg{press("/"), press("enter")}, wantCursor: 2},
		{name: "backspace on an empty filter restores the place", keys: []tea.KeyPressMsg{press("/"), press("backspace")}, wantCursor: 2},
		{name: "enter keeps a query", keys: []tea.KeyPressMsg{press("/"), press("z"), press("enter")}, wantFilter: "z", wantCursor: 0},
		{name: "typed text stays in the field", keys: []tea.KeyPressMsg{press("/"), press("z")}, wantFiltering: true, wantFilter: "z", wantCursor: 0},
		{name: "a space with no text is typed", keys: []tea.KeyPressMsg{press("/"), {Code: tea.KeySpace}}, wantFiltering: true, wantFilter: " ", wantCursor: 0},
		{name: "down leaves the field on the first row", keys: []tea.KeyPressMsg{press("/"), press("down")}, wantCursor: 0},
	}
	for _, overlay := range filterOverlays() {
		for _, tt := range tests {
			t.Run(overlay.name+"/"+tt.name, func(t *testing.T) {
				t.Cleanup(func() { applyThemeAll(theme.Default()) })
				m := overlay.open()
				for _, msg := range tt.keys {
					overlay.key(&m, msg)
				}
				l := overlay.list(&m)
				if l.filtering != tt.wantFiltering || l.filter != tt.wantFilter || l.cursor != tt.wantCursor {
					t.Fatalf("filtering, filter, cursor = %t, %q, %d; want %t, %q, %d",
						l.filtering, l.filter, l.cursor, tt.wantFiltering, tt.wantFilter, tt.wantCursor)
				}
			})
		}
	}
}

// An empty filter field shows every row, so its count matches the rows.
func TestEmptyFilterFieldCountsEveryRow(t *testing.T) {
	for _, overlay := range filterOverlays() {
		t.Run(overlay.name, func(t *testing.T) {
			m := overlay.open()
			slash, _ := keyPressFor("/")
			overlay.key(&m, slash)
			l := overlay.list(&m)
			if !l.filtering {
				t.Fatal("filter field did not open")
			}
			want := 3
			if overlay.name == "file browser" {
				want = 2 // the filter never shows the ".." row
			}
			if got := len(l.filtered); got != want {
				t.Fatalf("rows in the empty filter = %d, want %d", got, want)
			}
		})
	}
}
