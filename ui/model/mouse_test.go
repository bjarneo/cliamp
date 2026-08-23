package model

import (
	"fmt"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/bjarneo/cliamp/playlist"
	"github.com/bjarneo/cliamp/ui"
)

type mouseTestEngine struct {
	playbackFakeEngine
	duration    time.Duration
	seekCalls   []time.Duration
	volumeCalls []float64
}

func (e *mouseTestEngine) Seek(d time.Duration) error {
	e.seekCalls = append(e.seekCalls, d)
	return nil
}
func (e *mouseTestEngine) SetVolume(v float64)     { e.volumeCalls = append(e.volumeCalls, v) }
func (e *mouseTestEngine) Duration() time.Duration { return e.duration }
func (e *mouseTestEngine) Seekable() bool          { return e.duration > 0 }

func newMouseTestModel(engine *mouseTestEngine, tracks int) Model {
	pl := playlist.New()
	for i := range tracks {
		pl.Add(playlist.Track{
			Path:  fmt.Sprintf("/tmp/mouse-%d.mp3", i),
			Title: fmt.Sprintf("Track %d", i),
		})
	}
	m := Model{
		player:    engine,
		playlist:  pl,
		vis:       ui.NewVisualizer(44100),
		width:     100,
		height:    40,
		focus:     focusPlaylist,
		mouseHits: &mouseHitState{},
	}
	m.recomputeLayout()
	m.View()
	return m
}

func clickAt(m *Model, x, y int) {
	m.handleMouseClick(tea.MouseClickMsg{X: x, Y: y, Button: tea.MouseLeft})
}

func wheelAt(m *Model, x, y int, button tea.MouseButton) {
	m.handleMouseWheel(tea.MouseWheelMsg{X: x, Y: y, Button: button})
}

func findRegion(m Model, kind hitKind, idx int) hitRegion {
	for _, r := range m.mouseHits.regions {
		if r.kind == kind && r.idx == idx {
			return r
		}
	}
	return hitRegion{}
}

func TestViewEnablesCellMotionMouse(t *testing.T) {
	m := newMouseTestModel(&mouseTestEngine{}, 4)

	view := m.View()

	if view.MouseMode != tea.MouseModeCellMotion {
		t.Fatalf("mouse mode = %v, want MouseModeCellMotion", view.MouseMode)
	}
}

func TestClickPlaylistRowSelectsThenPlays(t *testing.T) {
	engine := &mouseTestEngine{}
	m := newMouseTestModel(engine, 8)

	r := findRegion(m, hitPlaylistRow, 3)
	if r.kind != hitPlaylistRow {
		t.Fatal("no region registered for track 3")
	}
	x := m.mouseHits.offX + r.x0 + 1
	y := m.mouseHits.offY + r.y0

	clickAt(&m, x, y)
	if m.plCursor != 3 {
		t.Fatalf("plCursor after first click = %d, want 3", m.plCursor)
	}
	if engine.playing {
		t.Fatal("engine started playing on selection click, want selection only")
	}

	clickAt(&m, x, y)
	if !engine.playing {
		t.Fatal("second click did not start playback")
	}
	if got := m.playlist.Index(); got != 3 {
		t.Fatalf("playing index = %d, want 3", got)
	}
	if len(engine.playCalls) != 1 || engine.playCalls[0] != "/tmp/mouse-3.mp3" {
		t.Fatalf("play calls = %v, want [/tmp/mouse-3.mp3]", engine.playCalls)
	}
}

func TestWheelScrollsPlaylistClamped(t *testing.T) {
	engine := &mouseTestEngine{}
	m := newMouseTestModel(engine, 8)

	var band hitRegion
	for _, r := range m.mouseHits.regions {
		if r.kind == hitBody {
			band = r
			break
		}
	}
	if band.kind != hitBody {
		t.Fatal("no body band registered for wheel scrolling")
	}
	x := m.mouseHits.offX + 2
	y := m.mouseHits.offY + band.y0

	wheelAt(&m, x, y, tea.MouseWheelDown)
	if m.plCursor != 1 {
		t.Fatalf("plCursor after wheel down = %d, want 1", m.plCursor)
	}

	wheelAt(&m, x, y, tea.MouseWheelUp)
	wheelAt(&m, x, y, tea.MouseWheelUp)
	if m.plCursor != 0 {
		t.Fatalf("wheel up wrapped below first row: plCursor = %d, want 0", m.plCursor)
	}

	for range 20 {
		wheelAt(&m, x, y, tea.MouseWheelDown)
	}
	if want := m.playlist.Len() - 1; m.plCursor != want {
		t.Fatalf("plCursor after many wheel downs = %d, want %d (clamped)", m.plCursor, want)
	}
}

func TestClickSeekBarSeeks(t *testing.T) {
	engine := &mouseTestEngine{duration: time.Hour}
	m := newMouseTestModel(engine, 4)
	m.cachedDur = time.Hour

	r := findRegion(m, hitSeekBar, 0)
	if r.kind != hitSeekBar {
		t.Fatal("no seek bar region registered")
	}
	x := m.mouseHits.offX + (r.x0+r.x1)/2
	y := m.mouseHits.offY + r.y0

	clickAt(&m, x, y)

	if len(engine.seekCalls) != 1 {
		t.Fatalf("seek call count = %d, want 1", len(engine.seekCalls))
	}
	got := engine.seekCalls[0]
	wantMin, wantMax := 29*time.Minute+55*time.Second, 30*time.Minute+5*time.Second
	if got < wantMin || got > wantMax {
		t.Fatalf("seek target = %v, want within [%v, %v]", got, wantMin, wantMax)
	}
}

func TestClickVolumeBarSetsVolume(t *testing.T) {
	engine := &mouseTestEngine{}
	m := newMouseTestModel(engine, 4)

	r := findRegion(m, hitVolumeBar, 0)
	if r.kind != hitVolumeBar {
		t.Fatal("no volume bar region registered")
	}

	clickAt(&m, m.mouseHits.offX+r.x1-1, m.mouseHits.offY+r.y0)
	if len(engine.volumeCalls) != 1 {
		t.Fatalf("volume call count = %d, want 1", len(engine.volumeCalls))
	}
	barW := r.x1 - r.x0
	wantVol := -50.0 + float64(barW-1)/float64(barW)*56.0
	if got := engine.volumeCalls[0]; got < wantVol-0.01 || got > wantVol+0.01 {
		t.Fatalf("volume at bar end = %v dB, want %v", got, wantVol)
	}

	engine.volumeCalls = nil
	clickAt(&m, m.mouseHits.offX+r.x0, m.mouseHits.offY+r.y0)
	if got := engine.volumeCalls[0]; got != -50.0 {
		t.Fatalf("volume at bar start = %v dB, want -50", got)
	}
}

func TestNoRowRegionsWhileOverlayOpen(t *testing.T) {
	engine := &mouseTestEngine{}
	m := newMouseTestModel(engine, 6)
	m.visPicker.visible = true
	m.View()

	for _, r := range m.mouseHits.regions {
		switch r.kind {
		case hitPlaylistRow, hitProviderRow:
			t.Fatalf("row region registered while overlay open: kind=%d idx=%d", r.kind, r.idx)
		}
	}
}

func TestUpdateDispatchesMouseClick(t *testing.T) {
	engine := &mouseTestEngine{}
	m := newMouseTestModel(engine, 8)

	r := findRegion(m, hitPlaylistRow, 2)
	out, _ := m.Update(tea.MouseClickMsg{
		X:      m.mouseHits.offX + 1,
		Y:      m.mouseHits.offY + r.y0,
		Button: tea.MouseLeft,
	})
	next := out.(Model)

	if next.plCursor != 2 {
		t.Fatalf("plCursor after Update click = %d, want 2", next.plCursor)
	}
}

func TestProviderRowsResolveSearchFilterIndices(t *testing.T) {
	playlists := []playlist.PlaylistInfo{
		{ID: "a", Name: "Alpha"},
		{ID: "b", Name: "Beta"},
		{ID: "c", Name: "Gamma"},
	}
	m := keybindingTestModel()
	m.player = &mouseTestEngine{}
	m.providerLists = playlists
	m.provCursor = 1
	m.width, m.height = 100, 40
	m.focus = focusProvider
	m.mouseHits = &mouseHitState{}
	m.recomputeLayout()
	m.View()

	rows := 0
	for _, r := range m.mouseHits.regions {
		if r.kind == hitProviderRow {
			rows++
			if r.idx < 0 || r.idx >= len(playlists) {
				t.Fatalf("provider row region index out of range: %d", r.idx)
			}
		}
	}
	if rows == 0 {
		t.Fatal("no provider row regions registered")
	}

	// Clicking a provider row selects it without loading tracks.
	target := -1
	for _, r := range m.mouseHits.regions {
		if r.kind == hitProviderRow && r.idx != m.provCursor {
			target = r.idx
			break
		}
	}
	clickAt(&m, m.mouseHits.offX+1, m.mouseHits.offY+findRegion(m, hitProviderRow, target).y0)
	if m.provCursor != target {
		t.Fatalf("provCursor after click = %d, want %d", m.provCursor, target)
	}
}
