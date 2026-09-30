package model

import (
	"strings"
	"testing"

	"github.com/bjarneo/cliamp/playlist"
	"github.com/bjarneo/cliamp/ui"
)

func albumStub(id, name string) playlist.Track {
	return playlist.Track{Title: name, Path: "spotify:album:" + id, ProviderMeta: map[string]string{
		playlist.MetaKind: playlist.MetaKindAlbum, playlist.MetaAlbumID: id,
	}}
}

// Search results lead with album placeholders: they open, never play.
func TestImmersiveSearchAlbumRowsOpenAlbums(t *testing.T) {
	items := trackItems([]playlist.Track{albumStub("al9", "Charm"), {Title: "Song", Path: "/s"}})
	if items[0].kind != immKindAlbum || items[0].id != "al9" {
		t.Fatalf("album placeholder item = %+v, want an album item with id al9", items[0])
	}
	if items[1].kind != immKindTrack || items[1].id != "1" {
		t.Fatalf("track item = %+v, want track index 1", items[1])
	}
}

func TestPlayableFromDropsAlbumPlaceholders(t *testing.T) {
	tracks := []playlist.Track{albumStub("a", "A"), {Path: "/1"}, albumStub("b", "B"), {Path: "/2"}}
	kept, at := playableFrom(tracks, 3)
	if len(kept) != 2 || at != 1 || kept[at].Path != "/2" {
		t.Fatalf("playableFrom = %d tracks, index %d; want 2 tracks, index 1 (/2)", len(kept), at)
	}
}

func TestImmersiveShuffleCyclesWithoutRecommender(t *testing.T) {
	m := immersiveModel(t)
	m.player = &playbackFakeEngine{}
	m.playlist.Replace([]playlist.Track{{Path: "/1"}, {Path: "/2"}})
	m.immCycleShuffle()
	if !m.playlist.Shuffled() {
		t.Fatal("first press should turn shuffle on")
	}
	// No provider owns the queue, so Smart Shuffle is unavailable and the
	// second press turns shuffle off instead of stalling.
	m.immCycleShuffle()
	if m.playlist.Shuffled() || m.playlist.Smart() {
		t.Fatalf("second press: shuffled=%v smart=%v, want both off", m.playlist.Shuffled(), m.playlist.Smart())
	}
}

func TestImmersiveKeymapOpensOverImmersive(t *testing.T) {
	m := immersiveModel(t)
	m.handleKey(keyMsg("?"))
	if !m.keymap.visible || m.activeScreen() != screenKeymap {
		t.Fatalf("keymap visible=%v screen=%d, want the keymap over immersive", m.keymap.visible, m.activeScreen())
	}
	var sawImmersive bool
	for _, e := range m.keymap.entries {
		if strings.Contains(e.action, "Canvas view") {
			sawImmersive = true
		}
	}
	if !sawImmersive {
		t.Fatal("keymap lacks the immersive key section")
	}
	m.handleKey(keyMsg("esc"))
	if m.activeScreen() != screenImmersive {
		t.Fatalf("closing the keymap left screen %d, want immersive", m.activeScreen())
	}
}

func TestImmersiveVCyclesVisualizerAndCCyclesCanvas(t *testing.T) {
	m := immersiveModel(t)
	m.vis = ui.NewVisualizer(44100)
	before := m.vis.Mode
	m.handleImmersiveKey(keyMsg("v"))
	if m.vis.Mode == before {
		t.Fatal("v did not change the visualizer mode")
	}
	if m.immersive.mode != immCanvasList {
		t.Fatal("v must not change the canvas view")
	}
	m.handleImmersiveKey(keyMsg("c"))
	if m.immersive.mode != immCanvasRows {
		t.Fatalf("c: canvas mode = %d, want rows", m.immersive.mode)
	}
}

func TestImmersiveSignInPrompt(t *testing.T) {
	m := immersiveModel(t)
	m.noteImmersiveLoadErr("Playlists", playlist.ErrNeedsAuth)
	g := m.immGeom()
	out := stripAnsi(strings.Join(m.renderImmCanvasInner(g.canvasIW, g.canvasIH), "\n"))
	if !strings.Contains(out, "Sign in to stub") {
		t.Fatalf("canvas does not prompt for sign-in:\n%s", out)
	}
}

// The queue panel runs down beside the controls to the progress bar.
func TestImmersiveQueueReachesProgressBar(t *testing.T) {
	m := immersiveModel(t)
	g := m.immGeom()
	if g.bodyY+g.leftH != g.seekY {
		t.Fatalf("left column ends at row %d, progress bar is at %d", g.bodyY+g.leftH, g.seekY)
	}
	lines := strings.Split(stripAnsi(m.renderImmersive()), "\n")
	if got := len(lines); got != g.h {
		t.Fatalf("frame has %d rows, want %d", got, g.h)
	}
	if !strings.HasPrefix(lines[g.seekY-1], "╰") {
		t.Fatalf("row above the progress bar should close the queue panel, got %q", lines[g.seekY-1])
	}
}
