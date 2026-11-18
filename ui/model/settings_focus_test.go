package model

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/bjarneo/cliamp/playlist"
)

func TestSettingsFocusCycle(t *testing.T) {
	full := []focusArea{focusPlaylist, focusProvPill, focusVolume, focusEQ, focusShuffle, focusRepeat, focusSpeed}
	oneRow := []focusArea{focusPlaylist, focusProvPill, focusEQ, focusShuffle, focusRepeat, focusSpeed, focusVolume}
	twoRows := []focusArea{focusPlaylist, focusProvPill, focusVolume, focusSpeed, focusEQ, focusShuffle, focusRepeat}
	tests := []struct {
		name          string
		width, height int
		setup         func(*Model)
		want          []focusArea
	}{
		{name: "two columns", width: 100, height: 30, want: full},
		{name: "stacked", width: 56, height: 16, want: twoRows},
		{name: "single source", width: 100, height: 30, setup: func(m *Model) { m.providers = m.providers[:1] },
			want: []focusArea{focusPlaylist, focusVolume, focusEQ, focusShuffle, focusRepeat, focusSpeed}},
		{name: "closed", width: 86, height: 24, setup: func(m *Model) { m.hideSettings = true }, want: oneRow},
		{name: "merged modes", width: 100, height: 12, want: full},
		{name: "short pane", width: 100, height: 10, want: full},
		{name: "smallest stacked", width: 40, height: 16, want: twoRows},
		{name: "stacked settings hidden", width: 40, height: 13, want: []focusArea{focusPlaylist}},
		{name: "too small", width: 39, height: 9, want: []focusArea{focusPlaylist}},
		{name: "simplified", width: 100, height: 30, setup: func(m *Model) { m.simplified = true }, want: []focusArea{focusPlaylist}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := newColumnTestModel(tt.width, tt.height)
			if tt.setup != nil {
				tt.setup(&m)
			}
			m.recomputeLayout()
			if got := m.mainFocusAreas(); !slices.Equal(got, tt.want) {
				t.Fatalf("focus areas = %v, want %v", got, tt.want)
			}
			for _, reverse := range []bool{false, true} {
				key := tea.KeyPressMsg{Code: tea.KeyTab}
				if reverse {
					key.Mod = tea.ModShift
				}
				for i := 1; i <= len(tt.want); i++ {
					idx := i % len(tt.want)
					if reverse {
						idx = (len(tt.want) - idx) % len(tt.want)
					}
					updated, _ := m.Update(key)
					m = updated.(Model)
					if m.focus != tt.want[idx] {
						t.Fatalf("%s step %d: focus = %s, want %s", key.String(), i, m.focus.label(), tt.want[idx].label())
					}
				}
			}
		})
	}
}

func TestSettingsFocusMatchesShedRows(t *testing.T) {
	m := newColumnTestModel(100, 30)
	for rows := 1; rows <= settingsPaneMaxRows; rows++ {
		m.layout.bodyRows = rows + 1
		pane := ansi.Strip(m.renderSettingsPane(rows))
		for _, setting := range []struct {
			focus focusArea
			label string
		}{{focusProvPill, "SRC"}, {focusVolume, "VOL"}, {focusEQ, "EQ"}, {focusShuffle, "SHF"}, {focusRepeat, "RPT"}, {focusSpeed, "SPD"}} {
			if got, want := m.mainFocusAllowed(setting.focus), strings.Contains(pane, setting.label); got != want {
				t.Fatalf("%d rows: %s focus allowed = %v, want %v", rows, setting.label, got, want)
			}
		}
	}
}

func TestSettingsFocusWithoutPlaylist(t *testing.T) {
	m := Model{}
	for _, focus := range []focusArea{focusShuffle, focusRepeat} {
		if m.mainFocusAllowed(focus) {
			t.Fatalf("%s is focusable without a playlist", focus.label())
		}
	}
}

func TestSettingsFocusFromProviderPane(t *testing.T) {
	for _, closed := range []bool{false, true} {
		for _, reverse := range []bool{false, true} {
			t.Run(fmt.Sprintf("closed=%v/reverse=%v", closed, reverse), func(t *testing.T) {
				m := newColumnTestModel(86, 24)
				m.hideSettings = closed
				m.focus = focusProvider
				m.recomputeLayout()
				if !m.usesContentFirstLayout() {
					t.Fatal("expected content-first provider layout")
				}
				key := tea.KeyPressMsg{Code: tea.KeyTab}
				want := focusProvPill
				if reverse {
					key.Mod = tea.ModShift
					want = focusSpeed // the pane ends with speed, the stacked row with volume
					if closed {
						want = focusVolume
					}
				}
				updated, _ := m.Update(key)
				m = updated.(Model)
				if m.focus != want || !m.mainFocusAllowed(m.focus) {
					t.Fatalf("focus = %s, want visible %s", m.focus.label(), want.label())
				}
			})
		}
	}
}

func TestSettingsFocusNormalizesHiddenControls(t *testing.T) {
	for _, focus := range newColumnTestModel(100, 30).mainFocusAreas()[1:] {
		t.Run(focus.label(), func(t *testing.T) {
			m := newColumnTestModel(100, 30)
			m.focus, m.prevFocus = focus, focus
			updated, _ := m.Update(tea.WindowSizeMsg{Width: 39, Height: 9})
			m = updated.(Model)
			if m.focus != focusPlaylist || m.prevFocus != focusPlaylist {
				t.Fatalf("hidden focus survived resize: current=%s previous=%s", m.focus.label(), m.prevFocus.label())
			}
		})
	}
	for _, focus := range []focusArea{focusShuffle, focusRepeat, focusSpeed} {
		m := newColumnTestModel(100, 30)
		m.focus, m.prevFocus = focus, focus
		updated, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 9})
		m = updated.(Model)
		if m.focus != focusPlaylist || m.prevFocus != focusPlaylist {
			t.Fatalf("shed %s kept focus", focus.label())
		}
	}
}

func TestSettingsFocusVisibleInEveryLayout(t *testing.T) {
	for _, tt := range []struct {
		name          string
		width, height int
		closed        bool
	}{{"two columns", 86, 24, false}, {"stacked", 56, 16, false}, {"closed", 86, 24, true}} {
		t.Run(tt.name, func(t *testing.T) {
			m := newColumnTestModel(tt.width, tt.height)
			m.hideSettings = tt.closed
			m.recomputeLayout()
			for _, focus := range m.mainFocusAreas()[1:] {
				m.focus = focus
				label := map[focusArea]string{focusProvPill: "SRC", focusVolume: "VOL", focusEQ: "EQ", focusShuffle: "SHF", focusRepeat: "RPT", focusSpeed: "SPD"}[focus]
				if frame := ansi.Strip(m.View().Content); !strings.Contains(frame, label+" \u25b8") {
					t.Fatalf("no focus marker for %s:\n%s", focus.label(), frame)
				}
			}
			m.focus = focusProvPill
			m.handleKey(tea.KeyPressMsg{Code: tea.KeyRight})
			if frame := ansi.Strip(m.View().Content); !strings.Contains(frame, m.providers[1].Name) {
				t.Fatalf("selected source is not visible:\n%s", frame)
			}
		})
	}
}

func TestSettingsFocusActions(t *testing.T) {
	for _, focus := range []focusArea{focusVolume, focusShuffle, focusRepeat} {
		for _, key := range []tea.KeyPressMsg{
			{Code: tea.KeyLeft}, {Code: tea.KeyRight}, {Code: tea.KeyUp}, {Code: tea.KeyDown},
			{Text: "h"}, {Text: "l"}, {Text: "k"}, {Text: "j"}, {Code: tea.KeyEnter},
			{Text: "+"}, {Text: "="}, {Text: "-"}, {Text: "z"}, {Text: "r"}, {Text: "m"},
		} {
			t.Run(focus.label()+"/"+key.String(), func(t *testing.T) {
				m := newColumnTestModel(100, 30)
				p := &playbackFakeEngine{}
				notifier := &fakeNotifier{}
				saver := &recordingSaver{}
				m.player, m.notifier, m.configSaver, m.focus = p, notifier, saver, focus
				m.plCursor = 2
				next, cmd := m.Update(key)
				m = next.(Model)
				s := key.String()
				back := slices.Contains([]string{"left", "down", "h", "j"}, s)
				arrow := back || slices.Contains([]string{"right", "up", "l", "k"}, s)
				modeChanged := false
				switch {
				case s == "+" || s == "=" || s == "-" || focus == focusVolume && arrow:
					want := 1.0
					if back || s == "-" {
						want = -1
					}
					if p.volume != want || len(notifier.updates) != 1 || notifier.updates[0].VolumeDB != want {
						t.Fatalf("volume = %v, notifications = %v, want %v", p.volume, notifier.updates, want)
					}
				case s == "z" || focus == focusShuffle && (arrow || s == "enter"):
					modeChanged = true
					if !m.playlist.Shuffled() || saver.saved["shuffle"] != "true" {
						t.Fatalf("shuffle was not toggled and persisted: %v", saver.saved)
					}
				case s == "r" || focus == focusRepeat && (arrow || s == "enter"):
					modeChanged = true
					want := playlist.RepeatAll
					if back {
						want = playlist.RepeatOne
					}
					if m.playlist.Repeat() != want || saver.saved["repeat"] != fmt.Sprintf("%q", want.String()) {
						t.Fatalf("repeat = %s, saved = %v, want %s", m.playlist.Repeat(), saver.saved, want)
					}
				case s == "m":
					if !p.mono {
						t.Fatal("global mono toggle was swallowed")
					}
				}
				// Nothing plays, so a mode change arms no gapless preload.
				if modeChanged && (cmd != nil || m.preloading || len(p.preloadCalls) != 0) {
					t.Fatal("mode change armed a gapless preload while nothing plays")
				}
				if m.plCursor != 2 || len(p.seekCalls) != 0 || m.buffering || m.focus != focus {
					t.Fatal("focused action moved the playlist, sought, played, or changed focus")
				}
			})
		}
	}
}

func TestSettingsFocusDoesNotStealOverlayTabs(t *testing.T) {
	for _, setup := range []struct {
		name string
		open func(*Model)
	}{
		{"keymap", func(m *Model) { m.openKeymap() }},
		{"search", func(m *Model) { m.handleKey(tea.KeyPressMsg{Text: "/"}) }},
		{"provider filter", func(m *Model) { m.focus = focusProvider; m.provSearch.active = true }},
		{"URL", func(m *Model) { m.urlInput.active = true }},
		{"track info", func(m *Model) { m.info.visible = true }},
	} {
		t.Run(setup.name, func(t *testing.T) {
			m := newColumnTestModel(100, 30)
			m.focus = focusVolume
			setup.open(&m)
			want := m.focus
			for _, key := range []tea.KeyPressMsg{{Code: tea.KeyTab}, {Code: tea.KeyTab, Mod: tea.ModShift}} {
				m.handleKey(key)
				if m.focus != want {
					t.Fatalf("%s changed overlay focus from %s to %s", key.String(), want.label(), m.focus.label())
				}
			}
		})
	}
}

func TestSettingsFocusHelpAndReservation(t *testing.T) {
	if !ReservedKeys()["shift+tab"] {
		t.Fatal("Shift+Tab is not reserved for core navigation")
	}
	for _, focus := range []focusArea{focusVolume, focusShuffle, focusRepeat} {
		m := newColumnTestModel(100, 30)
		m.focus = focus
		_, label := m.keymapContext()
		if label != focus.label() {
			t.Fatalf("keymap context = %q, want %q", label, focus.label())
		}
		if help := ansi.Strip(m.renderHelpBar()); !strings.Contains(help, "Tab") || !strings.Contains(help, "Cycle settings") {
			t.Fatalf("%s help should advertise Tab Cycle settings: %q", focus.label(), help)
		}
		help := ansi.Strip(m.renderHelpBar())
		if !strings.Contains(help, "↑↓") && !strings.Contains(help, "←→") {
			t.Fatalf("%s help is missing adjustment keys: %q", focus.label(), help)
		}
		entries := m.buildKeymapEntries()
		if len(entries) == 0 || !entries[0].divider || !strings.Contains(entries[0].action, label) {
			t.Fatalf("keymap did not start with the focused %s context", label)
		}
	}
	m := New(&playbackFakeEngine{}, playlist.New(), nil, "", nil, nil, nil, nil, nil, nil)
	if m.focus != focusPlaylist {
		t.Fatalf("startup focus = %s, want unchanged playlist focus", m.focus.label())
	}
}
