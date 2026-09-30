package model

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/bjarneo/cliamp/playlist"
)

// TestReservedKeysCoversHandleKey is a drift guard: every `case "..."` clause in
// the main handleKey switch (keys.go) must be represented in commandRegistry.
func TestReservedKeysCoversHandleKey(t *testing.T) {
	path := filepath.Join("keys.go")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	// Find every `case "X", "Y", ...:` clause in keys.go. We intentionally
	// over-collect (subhandler switches too) and then filter to the main
	// handler's section between "func (m *Model) handleKey" and its close.
	src := string(data)
	// The main dispatch switch is anchored by its comment header; overlays
	// and subhandlers have their own switches with different anchors.
	start := strings.Index(src, "// Vim-style count prefix")
	if start < 0 {
		t.Fatal("could not locate main dispatch anchor in keys.go")
	}
	// Bound at the next top-level function declaration to avoid scanning
	// into helper functions below handleKey.
	body := src[start:]
	if end := strings.Index(body, "\nfunc "); end > 0 {
		body = body[:end]
	}

	caseRe := regexp.MustCompile(`case ("[^"]+"(?:, "[^"]+")*):`)
	tokenRe := regexp.MustCompile(`"([^"]+)"`)
	reserved := ReservedKeys()

	var missing []string
	for _, m := range caseRe.FindAllStringSubmatch(body, -1) {
		for _, tok := range tokenRe.FindAllStringSubmatch(m[1], -1) {
			key := tok[1]
			if !reserved[key] {
				missing = append(missing, key)
			}
		}
	}

	if len(missing) > 0 {
		t.Fatalf("handleKey has case clauses not covered by coreReservedKeys: %v\nAdd these to keymap.go so plugin binds can't shadow them.", missing)
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
			check: func(m *Model) bool { return m.urlInputting },
		},
		{
			name: "playlist search from the playlist",
			key:  "/", action: "Filter/search list",
			check: func(m *Model) bool { return m.search.active },
		},
		{
			name: "jump to time from the playlist",
			key:  "Ctrl+J", action: "Jump to time",
			check: func(m *Model) bool { return m.jumping },
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
