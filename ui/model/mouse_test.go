package model

import (
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/bjarneo/cliamp/playlist"
)

// mouseTestModel builds a Model whose mouse geometry points at a 10-row body
// and a 74-cell seek bar, without rendering a full View.
func mouseTestModel(tracks int) Model {
	p := playlist.New()
	for i := range tracks {
		p.Add(playlist.Track{Title: string(rune('A' + i)), Path: "/t" + string(rune('a'+i)) + ".mp3"})
	}
	return Model{
		player:    &playbackFakeEngine{seekable: true, playing: true},
		playlist:  p,
		plVisible: 10,
		focus:     focusPlaylist,
		mouse: &mouseState{
			seekRow: 4, seekX: 10, seekW: 74,
			bodyRow: 8, bodyRows: 10, bodyX: 10, bodyW: 74,
		},
	}
}

func TestMouseSeekBarClickAndDrag(t *testing.T) {
	cases := []struct {
		name string
		col  int
	}{
		{"bar start", 0},
		{"bar quarter", 18},
		{"bar middle", 36},
		{"bar end clamps", 73},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := mouseTestModel(3)
			m.cachedDur = 2 * time.Minute
			fake := m.player.(*playbackFakeEngine)
			want := time.Duration(min(1.0, float64(tc.col)/float64(m.mouse.seekW-1)) * float64(m.cachedDur))

			m.handleMouseClick(tea.MouseClickMsg{X: m.mouse.seekX + tc.col, Y: m.mouse.seekRow, Button: tea.MouseLeft})

			if len(fake.seekCalls) != 1 {
				t.Fatalf("seek calls = %v, want 1", fake.seekCalls)
			}
			if got := fake.seekCalls[0]; got != want {
				t.Fatalf("seek target = %s, want %s", got, want)
			}
			if !m.mouse.dragging {
				t.Fatal("click on bar did not start a drag")
			}

			// Drag to the end, then release: the final position lands at the
			// release cell rather than a half-second late.
			m.handleMouseMotion(tea.MouseMotionMsg{X: m.mouse.seekX + m.mouse.seekW - 1, Y: m.mouse.seekRow})
			if got := fake.seekCalls[len(fake.seekCalls)-1]; got != 2*time.Minute {
				t.Fatalf("drag target = %s, want 2m", got)
			}
			m.handleMouseRelease()
			if m.mouse.dragging {
				t.Fatal("release did not end the drag")
			}
		})
	}
}

func TestMouseSeekBarClickIgnoresOtherButtonsAndRows(t *testing.T) {
	m := mouseTestModel(3)
	m.cachedDur = time.Minute
	fake := m.player.(*playbackFakeEngine)

	m.handleMouseClick(tea.MouseClickMsg{X: m.mouse.seekX + 5, Y: m.mouse.seekRow + 1, Button: tea.MouseLeft})
	m.handleMouseClick(tea.MouseClickMsg{X: m.mouse.seekX + 5, Y: m.mouse.seekRow, Button: tea.MouseRight})
	m.handleMouseClick(tea.MouseClickMsg{X: m.mouse.seekX + m.mouse.seekW + 10, Y: m.mouse.seekRow, Button: tea.MouseLeft})
	if len(fake.seekCalls) != 0 {
		t.Fatalf("seek calls = %v, want none", fake.seekCalls)
	}
	if m.mouse.dragging {
		t.Fatal("right click on bar started a drag")
	}
}

func TestMouseDragClampsPastBarEnd(t *testing.T) {
	m := mouseTestModel(3)
	m.cachedDur = 2 * time.Minute
	fake := m.player.(*playbackFakeEngine)
	m.handleMouseClick(tea.MouseClickMsg{X: m.mouse.seekX, Y: m.mouse.seekRow, Button: tea.MouseLeft})
	m.handleMouseMotion(tea.MouseMotionMsg{X: m.mouse.seekX + m.mouse.seekW + 50, Y: m.mouse.seekRow})
	if got := fake.seekCalls[len(fake.seekCalls)-1]; got != 2*time.Minute {
		t.Fatalf("clamped drag target = %s, want 2m", got)
	}
}

func TestMouseSeekBarNotSeekable(t *testing.T) {
	m := mouseTestModel(3)
	m.player = &playbackFakeEngine{seekable: false}
	m.cachedDur = time.Minute
	fake := m.player.(*playbackFakeEngine)

	m.handleMouseClick(tea.MouseClickMsg{X: m.mouse.seekX + 10, Y: m.mouse.seekRow, Button: tea.MouseLeft})
	if len(fake.seekCalls) != 0 {
		t.Fatalf("seek calls = %v, want none on unseekable player", fake.seekCalls)
	}
}

func TestMouseRightClickOpensMenuAndMovesCursor(t *testing.T) {
	m := mouseTestModel(4)

	m.handleMouseClick(tea.MouseClickMsg{X: m.mouse.bodyX + 1, Y: m.mouse.bodyRow + 2, Button: tea.MouseRight})

	if !m.trackMenu.visible {
		t.Fatal("right click did not open the track menu")
	}
	if m.plCursor != 2 {
		t.Fatalf("plCursor = %d, want 2", m.plCursor)
	}
	if m.trackMenu.track.Path != "/tc.mp3" {
		t.Fatalf("menu track = %q, want /tc.mp3", m.trackMenu.track.Path)
	}
	if m.trackMenu.remove != menuRemovePlaylist || m.trackMenu.removeIdx != 2 {
		t.Fatalf("remove = %v idx %d, want playlist idx 2", m.trackMenu.remove, m.trackMenu.removeIdx)
	}
}

func TestMouseLeftClickMovesCursor(t *testing.T) {
	m := mouseTestModel(4)
	m.handleMouseClick(tea.MouseClickMsg{X: m.mouse.bodyX + 1, Y: m.mouse.bodyRow + 1, Button: tea.MouseLeft})
	if m.plCursor != 1 {
		t.Fatalf("plCursor = %d, want 1", m.plCursor)
	}
	if m.trackMenu.visible {
		t.Fatal("left click opened the menu")
	}
}

func TestMouseClickOnEmptyRowNoop(t *testing.T) {
	m := mouseTestModel(1)
	m.handleMouseClick(tea.MouseClickMsg{X: m.mouse.bodyX + 1, Y: m.mouse.bodyRow + 9, Button: tea.MouseRight})
	if m.trackMenu.visible {
		t.Fatal("right click on empty row opened the menu")
	}
}

func TestMouseNilStateSafe(t *testing.T) {
	m := Model{}
	m.handleMouseClick(tea.MouseClickMsg{X: 1, Y: 1, Button: tea.MouseLeft})
	m.handleMouseMotion(tea.MouseMotionMsg{X: 1, Y: 1})
	m.handleMouseRelease()
	m.recordMouseGeometry("x", []string{"x"}, "x")
}

func TestMouseWheelScrollsPlaylist(t *testing.T) {
	m := mouseTestModel(10)
	m.handleMouseWheel(tea.MouseWheelMsg{X: m.mouse.bodyX + 1, Y: m.mouse.bodyRow + 1, Button: tea.MouseWheelDown})
	if m.plCursor != 3 {
		t.Fatalf("plCursor = %d, want 3", m.plCursor)
	}
	m.handleMouseWheel(tea.MouseWheelMsg{X: m.mouse.bodyX + 1, Y: m.mouse.bodyRow + 1, Button: tea.MouseWheelUp})
	if m.plCursor != 0 {
		t.Fatalf("plCursor = %d, want 0", m.plCursor)
	}
}

func TestHitPlaylistRowMatchesRenderMath(t *testing.T) {
	cases := []struct {
		name       string
		tracks     []playlist.Track
		headers    bool
		row        int
		provider   bool // force provider focus
		wantCursor int  // -1 = no track
		wantIdx    int
	}{
		{name: "first row", tracks: []playlist.Track{{Title: "A"}, {Title: "B"}}, row: 0, wantCursor: 0, wantIdx: 0},
		{name: "second row", tracks: []playlist.Track{{Title: "A"}, {Title: "B"}}, row: 1, wantCursor: 1, wantIdx: 1},
		{name: "past end", tracks: []playlist.Track{{Title: "A"}}, row: 5, wantCursor: -1},
		{name: "album header consumes row", tracks: []playlist.Track{{Album: "X", Title: "A"}, {Album: "X", Title: "B"}}, headers: true, row: 0, wantCursor: -1},
		{name: "track after header", tracks: []playlist.Track{{Album: "X", Title: "A"}, {Album: "X", Title: "B"}}, headers: true, row: 1, wantCursor: 0, wantIdx: 0},
		{name: "provider focus yields none", tracks: []playlist.Track{{Title: "A"}}, row: 0, provider: true, wantCursor: -1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := playlist.New()
			p.Replace(tc.tracks)
			m := Model{
				playlist:         p,
				player:           &playbackFakeEngine{},
				plVisible:        10,
				focus:            focusPlaylist,
				showAlbumHeaders: tc.headers,
				mouse:            &mouseState{},
			}
			if tc.provider {
				m.focus = focusProvider
			}
			hit := m.hitPlaylistRow(tc.row)
			if hit.cursor != tc.wantCursor {
				t.Fatalf("cursor = %d, want %d", hit.cursor, tc.wantCursor)
			}
			if tc.wantCursor >= 0 && (hit.remove != menuRemovePlaylist || hit.removeIdx != tc.wantIdx) {
				t.Fatalf("remove = %v idx %d, want playlist idx %d", hit.remove, hit.removeIdx, tc.wantIdx)
			}
		})
	}
}
