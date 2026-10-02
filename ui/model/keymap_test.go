package model

import (
	"slices"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/bjarneo/cliamp/playlist"
)

// mainKeyPath lists the functions of the main key path: handleKey without
// the overlays and the focused areas that own a command mode. The path ends
// in the plugin forward of handleMainKey. The test adds the keys of the
// provider shortcut helpers through shortcutKeys.
var mainKeyPath = []string{"handleKey", "handleGlobalKey", "handleMainKey"}

// focusKeyHandlers maps the handlers that handleKey calls for a focused area
// to the command mode of that area.
var focusKeyHandlers = map[string]commandMode{
	"handleProvSearchKey":   commandModeProviderSearch,
	"handleProviderPaneKey": commandModeProvider,
	"handleSpeedKey":        commandModeSpeed,
	"handleProvPillKey":     commandModeProviderPill,
}

// TestReservedKeysCoversHandleKey is a drift guard. Every key that the main
// key path handles must be in commandRegistry, so a plugin cannot bind a key
// that cliamp takes before the plugin forward. The keys of shortcutKeys
// count as keys of the function that calls the shortcut helper.
func TestReservedKeysCoversHandleKey(t *testing.T) {
	funcs := modelFuncs(t)
	reserved := ReservedKeys()
	for _, name := range mainKeyPath {
		fd := lookupFunc(t, funcs, name)
		keys := handlerKeys(t, fd)
		if len(keys) == 0 {
			t.Errorf("%s handles no keys. The walker cannot read it.", name)
		}
		keys = append(keys, shortcutKeys(t, funcs, methodCalls(fd))...)
		var missing []string
		for _, key := range keys {
			if !reserved[key] {
				missing = append(missing, key)
			}
		}
		if len(missing) > 0 {
			t.Errorf("%s handles keys that commandRegistry does not list: %q\nAdd them to command_registry.go so plugin binds cannot shadow them.", name, missing)
		}

		// Each handler that the function calls is on the main key path or
		// owns the command mode of a focused area.
		for _, callee := range handlerCalls(fd) {
			if !slices.Contains(mainKeyPath, callee) && focusKeyHandlers[callee] == 0 {
				t.Errorf("%s calls %s. Add it to mainKeyPath or focusKeyHandlers.", name, callee)
			}
		}
	}
}

// TestKeyPressForBuildsEveryRegistryKey makes sure the keymap can send every
// key in the command registry, plus the forms that plugins bind.
func TestKeyPressForBuildsEveryRegistryKey(t *testing.T) {
	keys := []string{"f5", "ctrl+e", "alt+x", "shift+tab"}
	for _, command := range commandRegistry {
		keys = append(keys, command.Keys...)
	}
	for _, key := range keys {
		msg, ok := keyPressFor(key)
		if !ok {
			t.Errorf("keyPressFor(%q) failed", key)
			continue
		}
		if got := msg.String(); got != key {
			t.Errorf("keyPressFor(%q).String() = %q", key, got)
		}
	}
	for _, key := range []string{"", "hyper+x", "notakey"} {
		if _, ok := keyPressFor(key); ok {
			t.Errorf("keyPressFor(%q) = ok, want failure", key)
		}
	}
}

// selectKeymapEntry opens the keymap and puts the cursor on the entry with
// the given key label and action.
func selectKeymapEntry(t *testing.T, m *Model, key, action string) {
	t.Helper()
	m.handleKey(tea.KeyPressMsg{Code: 'k', Mod: tea.ModCtrl})
	if !m.keymap.visible {
		t.Fatal("keymap did not open")
	}
	for i, entry := range m.keymap.entries {
		if !entry.divider && entry.key == key && entry.action == action {
			m.keymap.cursor = i
			return
		}
	}
	t.Fatalf("keymap has no entry %q %q: %+v", key, action, m.keymap.entries)
}

func TestKeymapEnterRunsSelectedCommand(t *testing.T) {
	tests := []struct {
		name        string
		setup       func(*Model)
		key, action string
		check       func(*Model) bool
	}{
		{
			name: "load URL from the playlist",
			key:  "u", action: "Load URL (stream/playlist)",
			check: func(m *Model) bool { return m.urlInput.active },
		},
		{
			name: "playlist search from the playlist",
			key:  "/", action: "Filter/search list",
			check: func(m *Model) bool { return m.search.active },
		},
		{
			name: "jump to time from the playlist",
			key:  "Ctrl+J", action: "Jump to time",
			check: func(m *Model) bool { return m.jump.active },
		},
		{
			name: "queue manager from the playlist",
			key:  "A", action: "Queue manager",
			check: func(m *Model) bool { return m.queue.visible },
		},
		{
			name: "back from the provider pane",
			setup: func(m *Model) {
				m.playlist.Add(playlist.Track{Title: "Song"})
				m.focus = focusProvider
			},
			key: "Esc", action: "Back",
			check: func(m *Model) bool { return m.focus == focusPlaylist },
		},
		{
			name:  "cancel an open playlist search",
			setup: func(m *Model) { m.search.active = true; m.focus = focusSearch },
			key:   "Esc", action: "Cancel",
			check: func(m *Model) bool { return !m.search.active },
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := keybindingTestModel()
			if tt.setup != nil {
				tt.setup(&m)
			}
			selectKeymapEntry(t, &m, tt.key, tt.action)

			m.handleKey(tea.KeyPressMsg{Code: tea.KeyEnter})

			if m.keymap.visible {
				t.Fatal("keymap.visible = true after Enter, want false")
			}
			if !tt.check(&m) {
				t.Fatalf("Enter on %q %q did not run the command", tt.key, tt.action)
			}
		})
	}
}

func TestKeymapEnterExplainsCommandsItCannotRun(t *testing.T) {
	tests := []struct {
		name        string
		setup       func(*Model)
		key, action string
		want        string
	}{
		{name: "key pair", key: "Left Right", action: "Seek +/-5s", want: "Close the keymap. Then press Left Right."},
		{name: "count prefix", key: "Nj", action: "Seek to N x 10% of track (e.g. 7j = 70%)", want: "Then press Nj."},
		{
			name:  "player command over the file browser",
			setup: func(m *Model) { m.fileBrowser.visible = true },
			key:   "s", action: "Stop",
			want: "Stop is not available in Files.",
		},
		{
			name:  "quit over the file browser",
			setup: func(m *Model) { m.fileBrowser.visible = true },
			key:   "q", action: "Quit",
			want: "Quit is not available in Files.",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := keybindingTestModel()
			if tt.setup != nil {
				tt.setup(&m)
			}
			selectKeymapEntry(t, &m, tt.key, tt.action)

			if cmd := m.handleKey(tea.KeyPressMsg{Code: tea.KeyEnter}); cmd != nil {
				t.Fatal("Enter returned a command for an entry it cannot run")
			}
			if !m.keymap.visible {
				t.Fatal("keymap closed for an entry it cannot run")
			}
			if !strings.Contains(m.status.text, tt.want) {
				t.Fatalf("status = %q, want %q", m.status.text, tt.want)
			}
		})
	}
}

// The keymap lists the commands of the playlist manager screen that is open,
// the same commands as that screen's help line.
func TestKeymapContextFollowsPlaylistManagerScreen(t *testing.T) {
	for _, tc := range []struct {
		name        string
		screen      plMgrScreenType
		wantMode    commandMode
		wantLabel   string
		wantRuns    []string
		wantMissing []string
	}{
		{name: "list", screen: plMgrScreenList, wantMode: commandModePlaylistManager, wantLabel: "Playlists", wantRuns: []string{"Select"}},
		{name: "tracks", screen: plMgrScreenTracks, wantMode: commandModePlaylistManager, wantLabel: "Playlists", wantRuns: []string{"Select"}},
		{
			name: "dirs", screen: plMgrScreenDirs,
			wantMode: commandModePlaylistManagerDirs, wantLabel: "Directory Sources",
			wantRuns:    []string{"Add dir", "Remove", "Toggle recursive"},
			wantMissing: []string{"Select", "Add to the current playlist"},
		},
		{name: "new name", screen: plMgrScreenNewName, wantMode: commandModePlaylistManagerInput, wantLabel: "Playlist Name"},
		{name: "rename", screen: plMgrScreenRename, wantMode: commandModePlaylistManagerInput, wantLabel: "Playlist Name"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := keybindingTestModel()
			m.plManager = plManagerState{visible: true, screen: tc.screen}

			mode, label := m.keymapContext()
			if mode != tc.wantMode || label != tc.wantLabel {
				t.Fatalf("keymapContext() = %v %q, want %v %q", mode, label, tc.wantMode, tc.wantLabel)
			}
			runs := map[string]bool{}
			for _, entry := range m.buildKeymapEntries() {
				if entry.run != "" {
					runs[entry.action] = true
				}
			}
			for _, action := range tc.wantRuns {
				if !runs[action] {
					t.Errorf("keymap cannot run %q on this screen", action)
				}
			}
			for _, action := range tc.wantMissing {
				if runs[action] {
					t.Errorf("keymap runs %q, which this screen does not handle", action)
				}
			}
		})
	}
}
