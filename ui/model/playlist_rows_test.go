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

const shuffleRowsTrackCount = 8

// shuffleRowsTestModel returns a model with the cursor on the playing track.
// With shuffle on, no row after the first lists the track with the same
// index, so a test fails if an action reads a view row as a track index.
func shuffleRowsTestModel(t *testing.T, shuffle bool) Model {
	t.Helper()
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
	m.layout.panelWidth = 80
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

// albumRowsTestModel returns a model with 3 albums of perAlbum tracks each
// and album headers on. The cursor is on the first track.
func albumRowsTestModel(t *testing.T, perAlbum, visible int) Model {
	t.Helper()
	tracks := make([]playlist.Track, 3*perAlbum)
	for i := range tracks {
		tracks[i] = playlist.Track{
			Path:  fmt.Sprintf("/music/track-%d.mp3", i),
			Title: fmt.Sprintf("Song %c", 'A'+i),
			Album: fmt.Sprintf("Album %d", i/perAlbum),
		}
	}
	m := playlistScrollTestModel(tracks, 0, 0, false)
	m.setHeaderStateFromTracks(tracks)
	if !m.showAlbumHeaders {
		t.Fatal("the album header heuristic did not turn on the headers")
	}
	m.player = &playbackFakeEngine{}
	m.layout.panelWidth = 80
	m.focus = focusPlaylist
	m.plVisible = visible
	return m
}

// playlistViewLines renders the playlist view and splits it into lines
// without styles. A line with a "─" is an album header.
func playlistViewLines(m Model) (lines []string, headers int) {
	lines = strings.Split(ansi.Strip(m.renderPlaylist()), "\n")
	for _, line := range lines {
		if strings.Contains(line, "─") {
			headers++
		}
	}
	return lines, headers
}

// wantCursorInView checks that the view has visible lines and that the
// line with the cursor marker lists the track under the cursor.
func wantCursorInView(t *testing.T, m Model, lines []string, visible int) {
	t.Helper()
	if len(lines) != visible {
		t.Fatalf("view has %d lines, want %d", len(lines), visible)
	}
	cursor, _ := m.playlist.Track(m.plCursor)
	for _, line := range lines {
		if strings.HasPrefix(line, ">") {
			if !strings.Contains(line, cursor.Title) {
				t.Fatalf("cursor line = %q, want %q", line, cursor.Title)
			}
			return
		}
	}
	t.Fatalf("no line has the cursor on %q:\n%s", cursor.Title, strings.Join(lines, "\n"))
}

func TestShuffleHidesAlbumHeaders(t *testing.T) {
	const perAlbum, visible = 3, 12
	tests := []struct {
		name        string
		shuffle     bool
		wantHeaders int
	}{
		{name: "shuffle off draws one header per album", wantHeaders: 3},
		{name: "shuffle on draws no headers", shuffle: true, wantHeaders: 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := albumRowsTestModel(t, perAlbum, visible)
			if tt.shuffle {
				m.playlist.ToggleShuffle()
			}
			lines, headers := playlistViewLines(m)
			if headers != tt.wantHeaders {
				t.Fatalf("view has %d album headers, want %d:\n%s", headers, tt.wantHeaders, strings.Join(lines, "\n"))
			}
			if !m.showAlbumHeaders {
				t.Fatal("shuffle changed the album header setting")
			}
			if !tt.shuffle {
				return
			}
			for row := range 3 * perAlbum {
				if !strings.Contains(lines[row], " · Album ") {
					t.Fatalf("row %d = %q, want the album after the track name", row, lines[row])
				}
			}
		})
	}
}

func TestShuffleToggleKeepsCursorInViewWithAlbumHeaders(t *testing.T) {
	const perAlbum, visible = 4, 6
	down := tea.KeyPressMsg{Code: tea.KeyDown}
	tests := []struct {
		name string
		keys []tea.KeyPressMsg
	}{
		{name: "cursor on the first track"},
		{name: "cursor in the middle", keys: []tea.KeyPressMsg{down, down, down, down, down}},
		{name: "cursor on the last track", keys: []tea.KeyPressMsg{{Code: tea.KeyEnd}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := albumRowsTestModel(t, perAlbum, visible)
			for _, key := range tt.keys {
				m.handleKey(key)
			}
			want := m.plCursor
			for _, shuffle := range []bool{false, true, false} {
				if m.playlist.Shuffled() != shuffle {
					m.handleKey(tea.KeyPressMsg{Code: 'z', Text: "z"})
				}
				if m.plCursor != want {
					t.Fatalf("shuffle=%t: cursor on track %d, want track %d", shuffle, m.plCursor, want)
				}
				lines, headers := playlistViewLines(m)
				if (headers > 0) == shuffle {
					t.Fatalf("shuffle=%t: view has %d album headers:\n%s", shuffle, headers, strings.Join(lines, "\n"))
				}
				wantCursorInView(t, m, lines, visible)
			}
		})
	}
}

func TestCtrlHWhileShuffleOn(t *testing.T) {
	tests := []struct {
		name        string
		shuffle     bool
		wantHeaders bool
		wantStatus  string
	}{
		{name: "shuffle off toggles the headers"},
		{name: "shuffle on keeps the setting and warns", shuffle: true, wantHeaders: true, wantStatus: shuffleHeadersWarning},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := albumRowsTestModel(t, 3, 12)
			if tt.shuffle {
				m.playlist.ToggleShuffle()
			}
			m.handleKey(tea.KeyPressMsg{Code: 'h', Mod: tea.ModCtrl})
			if m.showAlbumHeaders != tt.wantHeaders {
				t.Fatalf("showAlbumHeaders = %t, want %t", m.showAlbumHeaders, tt.wantHeaders)
			}
			if m.headerManual == tt.shuffle {
				t.Fatalf("headerManual = %t, want %t", m.headerManual, !tt.shuffle)
			}
			if m.status.text != tt.wantStatus {
				t.Fatalf("status = %q, want %q", m.status.text, tt.wantStatus)
			}
		})
	}
}

// Add mixes each page into the shuffle order, and each page updates the
// album header heuristic. While shuffle is on, the view draws no headers
// after any page. When shuffle is off, the headers follow the track order.
func TestShufflePagesDrawNoAlbumHeaders(t *testing.T) {
	var pages [][]playlist.Track
	for album := range 3 {
		page := make([]playlist.Track, 4)
		for i := range page {
			n := 4*album + i
			page[i] = playlist.Track{
				Path:  fmt.Sprintf("/music/track-%d.mp3", n),
				Title: fmt.Sprintf("Song %c", 'A'+n),
				Album: fmt.Sprintf("Album %d", album),
			}
		}
		pages = append(pages, page)
	}
	prov := &pagerProv{name: "Pager", pages: pages}
	m := newPagingModel(prov)
	m.playlist.ToggleShuffle()

	for i, page := range pages {
		updated, _ := m.Update(tracksLoadedMsg{
			tracks: page, playlistID: "list", providerName: "Pager", offset: i, next: (i + 1) % len(pages), gen: 1,
		})
		m = updated.(Model)
		m.layout.panelWidth = 80
		m.plVisible = 16
		if lines, headers := playlistViewLines(m); headers != 0 {
			t.Fatalf("page %d: view has %d album headers with shuffle on:\n%s", i, headers, strings.Join(lines, "\n"))
		}
	}
	if got := m.playlist.Len(); got != 12 {
		t.Fatalf("playlist has %d tracks, want 12", got)
	}

	m.handleKey(tea.KeyPressMsg{Code: 'z', Text: "z"})
	if m.playlist.Shuffled() {
		t.Fatal("z did not turn off shuffle")
	}
	if lines, headers := playlistViewLines(m); headers != 3 {
		t.Fatalf("view has %d album headers with shuffle off, want 3:\n%s", headers, strings.Join(lines, "\n"))
	}
}
