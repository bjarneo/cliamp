package model

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/bjarneo/cliamp/playlist"
	"github.com/bjarneo/cliamp/ui"
)

const shuffleRowsTrackCount = 8

// shuffleRowsTestModel returns a model with the cursor on the playing track.
// With shuffle on, no row after the first lists the track with the same
// index, so a test fails if an action reads a view row as a track index.
func shuffleRowsTestModel(t *testing.T, shuffle bool) Model {
	t.Helper()
	old := ui.PanelWidth
	ui.PanelWidth = 80
	t.Cleanup(func() { ui.PanelWidth = old })

	tracks := make([]playlist.Track, shuffleRowsTrackCount)
	for i := range tracks {
		tracks[i] = playlist.Track{
			Path:  fmt.Sprintf("/music/track-%d.mp3", i),
			Title: fmt.Sprintf("Song %c", 'A'+i),
		}
	}
	m := playlistScrollTestModel(tracks, 0, 0, false)
	if shuffle {
		m.playlist.ToggleShuffle()
		for !rowsDifferFromTracks(m.playlist) {
			m.playlist.ToggleShuffle()
			m.playlist.ToggleShuffle()
		}
	}
	m.player = &playbackFakeEngine{}
	m.focus = focusPlaylist
	m.plVisible = shuffleRowsTrackCount
	m.plCursor = m.playlist.Index()
	return m
}

// rowsDifferFromTracks reports whether no row after the first lists the track
// with the same index.
func rowsDifferFromTracks(pl *playlist.Playlist) bool {
	order, _ := pl.OrderWindow(0, pl.Len())
	for row, idx := range order[1:] {
		if idx == row+1 {
			return false
		}
	}
	return true
}

// wantCursorOnLastRow checks that the cursor is on the track at the last row.
func wantCursorOnLastRow(t *testing.T, m Model, order []int, _ []playlist.Track) {
	t.Helper()
	if want := order[len(order)-1]; m.plCursor != want {
		t.Fatalf("cursor on track %d, want track %d on the last row", m.plCursor, want)
	}
}

func TestPlaylistRowMapping(t *testing.T) {
	for _, shuffle := range []bool{false, true} {
		t.Run(fmt.Sprintf("shuffle=%t", shuffle), func(t *testing.T) {
			m := shuffleRowsTestModel(t, shuffle)
			order, tracks := m.playlist.OrderWindow(0, shuffleRowsTrackCount)
			if !shuffle && !slices.Equal(order, []int{0, 1, 2, 3, 4, 5, 6, 7}) {
				t.Fatalf("play order without shuffle = %v, want the track order", order)
			}

			for row, want := range order {
				m.setPlCursorRow(row)
				if m.plCursor != want {
					t.Fatalf("setPlCursorRow(%d) put the cursor on track %d, want %d", row, m.plCursor, want)
				}
				if got := m.plCursorRow(); got != row {
					t.Fatalf("plCursorRow() = %d, want %d", got, row)
				}
			}

			for row, idx := range order {
				if want := fmt.Sprintf("/music/track-%d.mp3", idx); tracks[row].Path != want {
					t.Fatalf("row %d lists %q, want %q", row, tracks[row].Path, want)
				}
			}
		})
	}
}

func TestPlaylistCursorActionsFollowShuffleOrder(t *testing.T) {
	tests := []struct {
		name  string
		keys  []tea.KeyPressMsg
		check func(t *testing.T, m Model, order []int, before []playlist.Track)
	}{
		{
			name: "enter plays the track under the cursor",
			keys: []tea.KeyPressMsg{{Code: tea.KeyDown}, {Code: tea.KeyEnter}},
			check: func(t *testing.T, m Model, order []int, _ []playlist.Track) {
				if got := m.playlist.Index(); got != order[1] {
					t.Fatalf("playing track %d, want track %d on row 1", got, order[1])
				}
			},
		},
		{
			name: "a queues the track under the cursor",
			keys: []tea.KeyPressMsg{{Code: tea.KeyDown}, {Code: 'a', Text: "a"}},
			check: func(t *testing.T, m Model, order []int, _ []playlist.Track) {
				if got := m.playlist.QueuePosition(order[1]); got != 1 || m.playlist.QueueLen() != 1 {
					t.Fatalf("QueuePosition(%d) = %d with queue length %d, want 1 and 1", order[1], got, m.playlist.QueueLen())
				}
			},
		},
		{
			name: "x removes the track under the cursor and keeps the row",
			keys: []tea.KeyPressMsg{{Code: tea.KeyDown}, {Code: 'x', Text: "x"}},
			check: func(t *testing.T, m Model, order []int, before []playlist.Track) {
				removed := before[order[1]].Path
				for _, track := range m.playlist.Tracks() {
					if track.Path == removed {
						t.Fatalf("track %q is still in the playlist", removed)
					}
				}
				if m.playlist.Len() != shuffleRowsTrackCount-1 {
					t.Fatalf("playlist length = %d, want %d", m.playlist.Len(), shuffleRowsTrackCount-1)
				}
				cursor, _ := m.playlist.Track(m.plCursor)
				if want := before[order[2]].Path; cursor.Path != want || m.plCursorRow() != 1 {
					t.Fatalf("cursor on %q at row %d, want %q at row 1", cursor.Path, m.plCursorRow(), want)
				}
			},
		},
		{
			name:  "end moves the cursor to the last row",
			keys:  []tea.KeyPressMsg{{Code: tea.KeyEnd}},
			check: wantCursorOnLastRow,
		},
		{
			name:  "up from the first row wraps to the last row",
			keys:  []tea.KeyPressMsg{{Code: tea.KeyUp}},
			check: wantCursorOnLastRow,
		},
		{
			name: "shift+down does not move a track",
			keys: []tea.KeyPressMsg{{Code: tea.KeyDown}, {Code: tea.KeyDown, Mod: tea.ModShift}},
			check: func(t *testing.T, m Model, order []int, before []playlist.Track) {
				if got := m.playlist.Tracks(); !slices.EqualFunc(got, before, func(a, b playlist.Track) bool { return a.Path == b.Path }) {
					t.Fatal("Shift+Down changed the track order while shuffle is on")
				}
				if m.plCursor != order[1] {
					t.Fatalf("cursor on track %d, want track %d", m.plCursor, order[1])
				}
				if m.status.text != shuffleMoveWarning {
					t.Fatalf("status = %q, want %q", m.status.text, shuffleMoveWarning)
				}
			},
		},
		{
			name: "the view marks the track under the cursor",
			keys: []tea.KeyPressMsg{{Code: tea.KeyDown}, {Code: tea.KeyDown}},
			check: func(t *testing.T, m Model, order []int, before []playlist.Track) {
				lines := strings.Split(ansi.Strip(m.renderPlaylist()), "\n")
				for row, idx := range order {
					if !strings.Contains(lines[row], before[idx].Title) {
						t.Fatalf("row %d = %q, want %q", row, lines[row], before[idx].Title)
					}
					if selected := strings.HasPrefix(lines[row], ">"); selected != (row == 2) {
						t.Fatalf("row %d = %q, cursor marker %t, want %t", row, lines[row], selected, row == 2)
					}
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := shuffleRowsTestModel(t, true)
			order, _ := m.playlist.OrderWindow(0, shuffleRowsTrackCount)
			before := m.playlist.Tracks()
			if m.plCursorRow() != 0 {
				t.Fatalf("cursor starts at row %d, want row 0", m.plCursorRow())
			}
			for _, key := range tt.keys {
				m.handleKey(key)
			}
			tt.check(t, m, order, before)
		})
	}
}

func TestShuffleOffKeepsCursorOnTrack(t *testing.T) {
	m := shuffleRowsTestModel(t, true)
	m.handleKey(tea.KeyPressMsg{Code: tea.KeyDown})
	m.handleKey(tea.KeyPressMsg{Code: tea.KeyDown})
	want := m.plCursor

	m.handleKey(tea.KeyPressMsg{Code: 'z', Text: "z"})
	if m.playlist.Shuffled() {
		t.Fatal("z did not turn off shuffle")
	}
	if m.plCursor != want || m.plCursorRow() != want {
		t.Fatalf("cursor on track %d at row %d, want track %d at row %d", m.plCursor, m.plCursorRow(), want, want)
	}
}
