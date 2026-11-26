package model

import (
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/bjarneo/cliamp/playlist"
)

// updatableDirProvider saves the playlist edits that the tracks screen makes.
type updatableDirProvider struct{ *dirSourceTestProvider }

func (updatableDirProvider) UpdatePlaylist(_ string, fn func([]playlist.Track) ([]playlist.Track, error)) error {
	_, err := fn([]playlist.Track{{Path: "/a.mp3"}, {Path: "/b.mp3"}})
	return err
}

// d opens the audio device picker on the playlist manager screens and in the
// queue overlay. x removes. The picker gets the keys first and closes back to
// the same screen and cursor.
func TestDeviceKeyOnOverlays(t *testing.T) {
	prov := &dirSourceTestProvider{commandsTestProvider: commandsTestProvider{
		name:  "Local",
		lists: []playlist.PlaylistInfo{{ID: "music", Name: "music"}, {ID: "top40", Name: "top40"}},
	}}
	newModel := func(t *testing.T) Model {
		m := newDirsScreenTestModel(t, updatableDirProvider{prov})
		m.plManager.playlists = prov.lists
		m.plManager.cursor = 1
		m.plManager.dirs = []playlist.DirSource{{Path: "/x"}, {Path: "/y"}}
		m.plManager.tracks = []playlist.Track{{Path: "/a.mp3", Title: "A"}, {Path: "/b.mp3", Title: "B"}}
		m.playlist.Add(playlist.Track{Path: "/a.mp3"}, playlist.Track{Path: "/b.mp3"})
		m.playlist.Queue(0)
		m.playlist.Queue(1)
		m.plManager.missingLocal = make([]bool, 2)
		m.devicePicker.devices = nil
		m.player = &playbackFakeEngine{}
		return m
	}
	for _, tc := range []struct {
		name    string
		setup   func(m *Model)
		cursor  func(m *Model) int
		removed func(m *Model) bool
		confirm bool
	}{
		{"list", func(m *Model) { m.plManager.screen = plMgrScreenList },
			func(m *Model) int { return m.plManager.cursor }, nil, true},
		{"tracks", func(m *Model) { m.plManager.screen = plMgrScreenTracks },
			func(m *Model) int { return m.plManager.cursor }, nil, false},
		{"dirs", func(m *Model) { m.plManager.screen = plMgrScreenDirs },
			func(m *Model) int { return m.plManager.cursor }, nil, true},
		{"queue", func(m *Model) { m.plManager.visible = false; m.queue.visible = true },
			func(m *Model) int { return m.queue.cursor }, func(m *Model) bool { return m.playlist.QueueLen() == 1 }, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := newModel(t)
			tc.setup(&m)
			screen := m.activeScreen()
			cursor := tc.cursor(&m)

			// d opens the picker above the overlay and changes nothing else.
			m.handleKey(tea.KeyPressMsg{Text: "d", Code: 'd'})
			if !m.devicePicker.visible || m.activeScreen() != screenDevicePicker {
				t.Fatalf("d did not open the device picker, screen %v", m.activeScreen())
			}
			if m.plManager.confirmDel || m.playlist.QueueLen() != 2 || len(m.plManager.tracks) != 2 {
				t.Fatal("d changed data")
			}
			// The picker takes the keys. Esc closes it and the overlay returns.
			m.handleKey(tea.KeyPressMsg{Code: tea.KeyEscape})
			if m.devicePicker.visible || m.activeScreen() != screen || tc.cursor(&m) != cursor {
				t.Fatalf("picker did not return to screen %v cursor %d", screen, cursor)
			}
			for _, key := range []string{"d", "q"} {
				m.handleKey(tea.KeyPressMsg{Text: "d", Code: 'd'})
				m.handleKey(tea.KeyPressMsg{Text: key, Code: rune(key[0])})
				if m.devicePicker.visible || m.activeScreen() != screen {
					t.Fatalf("%s did not close the picker", key)
				}
			}

			// x removes, as d did before.
			m.handleKey(tea.KeyPressMsg{Text: "x", Code: 'x'})
			switch tc.name {
			case "tracks":
				if len(m.plManager.tracks) != 1 {
					t.Fatalf("x left %d tracks, want 1", len(m.plManager.tracks))
				}
			case "queue":
				if !tc.removed(&m) {
					t.Fatalf("x left %d queued, want 1", m.playlist.QueueLen())
				}
			default:
				if !m.plManager.confirmDel {
					t.Fatal("x did not ask the y/n prompt")
				}
			}
		})
	}
}

// d is text in the filter, the new name and the rename input.
func TestDeviceKeyIgnoredInTextInput(t *testing.T) {
	for _, tc := range []struct {
		name  string
		setup func(m *Model)
		text  func(m *Model) string
	}{
		{"filter", func(m *Model) { m.plManager.screen = plMgrScreenList; m.plManager.filtering = true },
			func(m *Model) string { return m.plManager.filter }},
		{"new name", func(m *Model) { m.plManager.screen = plMgrScreenNewName },
			func(m *Model) string { return m.plManager.newName }},
		{"rename", func(m *Model) { m.plManager.screen = plMgrScreenRename },
			func(m *Model) string { return m.plManager.renameName }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := newDirsScreenTestModel(t, &dirSourceTestProvider{commandsTestProvider: commandsTestProvider{name: "Local"}})
			tc.setup(&m)
			m.handleKey(tea.KeyPressMsg{Text: "d", Code: 'd'})
			if m.devicePicker.visible {
				t.Fatal("d opened the device picker during text input")
			}
			if got := tc.text(&m); got != "d" {
				t.Fatalf("text = %q, want d", got)
			}
		})
	}
}
