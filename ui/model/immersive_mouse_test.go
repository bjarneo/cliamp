package model

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/bjarneo/cliamp/playlist"
)

// immProvStub is a minimal playlist.Provider for click-to-open tests.
type immProvStub struct{}

func (immProvStub) Name() string { return "stub" }

func (immProvStub) Playlists() ([]playlist.PlaylistInfo, error) { return nil, nil }

func (immProvStub) Tracks(string) ([]playlist.Track, error) { return nil, nil }

// immersiveMouseModel builds an immersive model with mouse state attached and
// records a frame, so clicks hit the geometry View just drew.
func immersiveMouseModel(t *testing.T) *Model {
	t.Helper()
	m := immersiveModel(t)
	m.mouse = &mouseState{}
	m.immMouse = &immMouseGeom{}
	m.player = &playbackFakeEngine{seekable: true}
	m.cachedDur = 3 * time.Minute
	m.immersive.prov = immProvStub{}
	m.View()
	if m.immMouse == nil || !m.immMouse.valid {
		t.Fatal("View did not record immersive mouse geometry")
	}
	return m
}

func immClick(m *Model, cx, cyRel int, pane string, btn tea.MouseButton) tea.Cmd {
	im := m.immMouse
	x := im.frameX + cx
	switch pane {
	case "center":
		x = im.frameX + im.centerX + cx
	case "right":
		x = im.frameX + im.rightX + cx
	}
	return m.handleMouseClick(tea.MouseClickMsg{X: x, Y: im.bodyTop + cyRel, Button: btn})
}

func TestImmersiveSeekClickAndDrag(t *testing.T) {
	m := immersiveMouseModel(t)
	fake := m.player.(*playbackFakeEngine)
	col := m.mouse.seekW / 2
	want := time.Duration(float64(col) / float64(m.mouse.seekW-1) * float64(m.cachedDur))

	m.handleMouseClick(tea.MouseClickMsg{X: m.mouse.seekX + col, Y: m.mouse.seekRow, Button: tea.MouseLeft})

	if len(fake.seekCalls) != 1 {
		t.Fatalf("seek calls = %d, want 1", len(fake.seekCalls))
	}
	if got := fake.seekCalls[0]; got != want {
		t.Fatalf("seek target = %s, want %s", got, want)
	}
	if !m.mouse.dragging {
		t.Fatal("click on immersive seek bar did not start a drag")
	}
	m.handleMouseMotion(tea.MouseMotionMsg{X: m.mouse.seekX + m.mouse.seekW - 1, Y: m.mouse.seekRow})
	if got := fake.seekCalls[len(fake.seekCalls)-1]; got != m.cachedDur {
		t.Fatalf("drag target = %s, want %s", got, m.cachedDur)
	}
	m.handleMouseRelease()
	if m.mouse.dragging {
		t.Fatal("release did not end the drag")
	}
}

func TestImmersiveRailClickOpens(t *testing.T) {
	m := immersiveMouseModel(t)

	immClick(m, 2, immRailRowsTop, "rail", tea.MouseLeft)

	if m.immersive.view != immViewPlaylist {
		t.Fatalf("view = %d, want playlist view", m.immersive.view)
	}
	if !m.immersive.tracksLoading {
		t.Fatal("click did not start loading the playlist tracks")
	}
	if m.immersive.ctxID != "pl1" {
		t.Fatalf("ctxID = %q, want pl1", m.immersive.ctxID)
	}
}

func TestImmersiveRailRightClickSelectsOnly(t *testing.T) {
	m := immersiveMouseModel(t)

	immClick(m, 2, immRailRowsTop+2, "rail", tea.MouseRight)

	if m.immersive.focus != immPaneRail {
		t.Fatalf("focus = %d, want rail", m.immersive.focus)
	}
	if m.immersive.railCursor != 1 {
		t.Fatalf("railCursor = %d, want 1", m.immersive.railCursor)
	}
	if m.immersive.view != immViewHome {
		t.Fatalf("view = %d, want home (right click must not open)", m.immersive.view)
	}
}

func TestImmersiveRailClickRespectsScroll(t *testing.T) {
	m := immersiveMouseModel(t)
	for range 30 {
		m.immersive.lists = append(m.immersive.lists, playlist.PlaylistInfo{ID: "px", Name: "pad"})
	}
	m.immersive.railCursor = 20
	m.View()

	rows := m.railRows()
	budget := max(1, (m.immMouse.bodyRows-immRailRowsTop)/2)
	want := clampedScroll(0, 20, len(rows), budget)
	immClick(m, 2, immRailRowsTop, "rail", tea.MouseRight)

	if m.immersive.railCursor != want {
		t.Fatalf("railCursor = %d, want scrolled row %d", m.immersive.railCursor, want)
	}
}

func TestImmersiveTableClickPlays(t *testing.T) {
	m := immersiveMouseModel(t)
	m.immersive.view = immViewPlaylist
	m.immersive.ctxID = "pl1"
	m.immersive.tracks = []playlist.Track{
		{Title: "one", Path: "/one.mp3"},
		{Title: "two", Path: "/two.mp3"},
		{Title: "three", Path: "/three.mp3"},
	}
	m.View()

	immClick(m, 4, immTableRowsTop+1, "center", tea.MouseLeft)

	if m.immersive.trackCursor != 1 {
		t.Fatalf("trackCursor = %d, want 1", m.immersive.trackCursor)
	}
	if m.playlist.Len() != 3 {
		t.Fatalf("player playlist len = %d, want 3", m.playlist.Len())
	}
	fake := m.player.(*playbackFakeEngine)
	if len(fake.playCalls) != 1 || fake.playCalls[0] != "/two.mp3" {
		t.Fatalf("playCalls = %v, want [/two.mp3]", fake.playCalls)
	}
}

func TestImmersiveTableRightClickQueues(t *testing.T) {
	m := immersiveMouseModel(t)
	m.immersive.view = immViewPlaylist
	m.immersive.tracks = []playlist.Track{
		{Title: "one", Path: "/one.mp3"},
		{Title: "two", Path: "/two.mp3"},
	}
	m.View()

	immClick(m, 4, immTableRowsTop, "center", tea.MouseRight)

	if m.playlist.QueueLen() != 1 {
		t.Fatalf("queue len = %d, want 1", m.playlist.QueueLen())
	}
	if m.immersive.trackCursor != 0 {
		t.Fatalf("trackCursor = %d, want 0", m.immersive.trackCursor)
	}
}

func TestImmersiveGridClickOpensCard(t *testing.T) {
	m := immersiveMouseModel(t)
	stripRows := len(m.renderImmRolodexStrip(m.immMouse.centerW))
	gridTop := stripRows + 3

	immClick(m, 1, gridTop, "center", tea.MouseLeft)

	if m.immersive.view != immViewPlaylist {
		t.Fatalf("view = %d, want playlist view", m.immersive.view)
	}
	if m.immersive.ctxID != "pl1" {
		t.Fatalf("ctxID = %q, want pl1", m.immersive.ctxID)
	}
}

func TestImmersiveStripClickSpinsAndOpens(t *testing.T) {
	m := immersiveMouseModel(t)

	// Click a neighbor column: the cursor moves there without opening.
	m.immersive.roloCursor = 0
	items := m.roloItems()
	w := m.immMouse.centerW
	neighW := clampInt(w/9, 8, 14)
	focusW := clampInt(w/4, 14, 22)
	totalW := 0
	for _, c := range []int{neighW, neighW, focusW, neighW, neighW} {
		totalW += c + 1
	}
	pad := max(0, (w-totalW)/2)
	rightX := pad + (neighW + 1) + (neighW + 1) + (focusW + 1) + 1
	immClick(m, rightX, 2, "center", tea.MouseLeft)

	if m.immersive.roloCursor != wrapIndex(1, len(items)) {
		t.Fatalf("roloCursor = %d, want 1", m.immersive.roloCursor)
	}
	if m.immersive.view != immViewHome {
		t.Fatalf("view = %d, want home (neighbor click must not open)", m.immersive.view)
	}

	// Click the focused card: it opens.
	focusX := pad + (neighW + 1) + (neighW + 1) + 1
	immClick(m, focusX, 2, "center", tea.MouseLeft)
	if m.immersive.view == immViewHome {
		t.Fatal("focused strip click did not open the card")
	}
}

func TestImmersiveTransportClickActsAsKey(t *testing.T) {
	m := immersiveMouseModel(t)
	im := m.immMouse
	transportRow := im.topRow + immBodyTopRel + im.bodyRows + 1
	for _, b := range m.immTransportButtons() {
		if b.key != "z" && b.key != "r" {
			continue
		}
		m.handleMouseClick(tea.MouseClickMsg{X: im.frameX + b.x0, Y: transportRow, Button: tea.MouseLeft})
	}
	if !m.playlist.Shuffled() {
		t.Fatal("shuffle button click did not toggle shuffle")
	}
	if m.playlist.Repeat() != 1 {
		t.Fatalf("repeat = %d, want 1", m.playlist.Repeat())
	}
}

func TestImmersiveTopBarClicks(t *testing.T) {
	m := immersiveMouseModel(t)
	im := m.immMouse
	top := im.topRow

	// Home button returns to the home view.
	m.immersive.view = immViewSearch
	m.handleMouseClick(tea.MouseClickMsg{X: im.frameX + 5, Y: top, Button: tea.MouseLeft})
	if m.immersive.view != immViewHome {
		t.Fatalf("view = %d, want home", m.immersive.view)
	}

	// Back button pops the nav stack.
	m.immersive.view = immViewPlaylist
	m.immersive.back = []immNavSnap{{view: immViewHome}}
	m.handleMouseClick(tea.MouseClickMsg{X: im.frameX, Y: top, Button: tea.MouseLeft})
	if m.immersive.view != immViewHome {
		t.Fatalf("view = %d, want home after back", m.immersive.view)
	}

	// Far-right click exits the mode.
	m.immersive.back = nil
	m.immersive.view = immViewHome
	m.handleMouseClick(tea.MouseClickMsg{X: im.frameX + im.w - 1, Y: top, Button: tea.MouseLeft})
	if m.immersive.active {
		t.Fatal("exit click did not leave immersive mode")
	}
}

func TestImmersiveRightRailClicks(t *testing.T) {
	if testing.Short() {
		t.Skip("needs right rail")
	}
	m := immersiveMouseModel(t)
	if m.immMouse.rightW == 0 {
		t.Skip("right rail not rendered at this width")
	}
	// Second tab switches to the queue.
	immClick(m, len(" Now playing ")+1, 0, "right", tea.MouseLeft)
	if m.immersive.rightTab != immTabQueue {
		t.Fatalf("rightTab = %d, want queue", m.immersive.rightTab)
	}
	// First tab switches back and stays put on repeat clicks.
	immClick(m, 1, 0, "right", tea.MouseLeft)
	if m.immersive.rightTab != immTabNowPlaying {
		t.Fatalf("rightTab = %d, want now playing", m.immersive.rightTab)
	}
}

func TestImmersiveClickOutsideFrameNoop(t *testing.T) {
	m := immersiveMouseModel(t)
	im := m.immMouse

	m.handleMouseClick(tea.MouseClickMsg{X: im.frameX - 1, Y: im.bodyTop, Button: tea.MouseLeft})
	m.handleMouseClick(tea.MouseClickMsg{X: im.frameX + im.w, Y: im.bodyTop, Button: tea.MouseLeft})
	m.handleMouseClick(tea.MouseClickMsg{X: im.frameX + 1, Y: im.topRow - 1, Button: tea.MouseLeft})

	got := m.immersive
	if !got.active || got.view != immViewHome || got.focus != immPaneCenter ||
		got.railCursor != 0 || got.trackCursor != 0 || got.gridCursor != 0 || got.roloCursor != 0 {
		t.Fatalf("out-of-frame click mutated immersive state: %+v", got)
	}
	if m.mouse.dragging {
		t.Fatal("out-of-frame click started a drag")
	}
}

func TestImmersiveMiddleClickNoop(t *testing.T) {
	m := immersiveMouseModel(t)

	immClick(m, 2, immRailRowsTop, "rail", tea.MouseMiddle)

	if m.immersive.view != immViewHome || m.immersive.focus != immPaneCenter {
		t.Fatal("middle click acted like a left click")
	}
	if m.mouse.dragging {
		t.Fatal("middle click started a seek drag")
	}
}

func TestImmersiveRailBlankAreaNoop(t *testing.T) {
	m := immersiveMouseModel(t) // 3 rows; the rail pane is much taller

	immClick(m, 2, m.immMouse.bodyRows-1, "rail", tea.MouseLeft)

	if m.immersive.view != immViewHome {
		t.Fatalf("view = %d, want home (blank click must not open)", m.immersive.view)
	}
	if m.immersive.focus != immPaneCenter || m.immersive.railCursor != 0 {
		t.Fatal("blank rail click moved the cursor")
	}
}

func TestImmersiveSpinnerAdvancesWhileLoading(t *testing.T) {
	m := immersiveModel(t)
	m.immersive.tracksLoading = true
	m.tickImmersive(time.Second)
	if m.immersive.spin != 1 {
		t.Fatalf("spin = %d, want 1", m.immersive.spin)
	}
	m.immersive.tracksLoading = false
	m.tickImmersive(time.Second)
	if m.immersive.spin != 1 {
		t.Fatalf("spin = %d, want still 1 without loading", m.immersive.spin)
	}
}

func TestImmersiveLoadingLineNamesContext(t *testing.T) {
	m := immersiveModel(t)
	m.immersive.view = immViewPlaylist
	m.immersive.ctxName = "Stuff to listen 2"
	m.immersive.tracksLoading = true
	lines := m.renderImmTrackView(60, 20, false)
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "loading Stuff to listen 2") {
		t.Fatalf("track view missing named loading line:\n%s", joined)
	}
	if !strings.Contains(joined, m.immSpin()) {
		t.Fatalf("track view missing spinner frame:\n%s", joined)
	}
}

func TestImmersiveTableRowsTopMatchesRender(t *testing.T) {
	m := immersiveModel(t)
	m.immersive.view = immViewPlaylist
	m.immersive.tracks = []playlist.Track{{Title: "one", Path: "/one.mp3"}}
	lines := m.renderImmTrackView(60, 20, false)
	if len(lines) <= immTableRowsTop {
		t.Fatalf("rendered %d lines, want more than table top %d", len(lines), immTableRowsTop)
	}
	if !strings.Contains(lines[immTableRowsTop], "one") {
		t.Fatalf("line %d = %q, want first track row", immTableRowsTop, lines[immTableRowsTop])
	}
}
