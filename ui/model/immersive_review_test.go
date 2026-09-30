package model

import (
	"os"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/bjarneo/cliamp/playlist"
	"github.com/bjarneo/cliamp/provider"
	"github.com/bjarneo/cliamp/ui/termimg"
)

// A late artist result must not land in the playlist view opened after it.
func TestImmersiveStaleArtistResultDropped(t *testing.T) {
	m := immersiveModel(t)
	m.openImmersiveItem(immItem{kind: immKindArtist, id: "ar1", title: "Lizzo"})
	artistGen := m.requests.immersiveArtist
	m.immersiveGoBack()
	m.openImmersiveItem(immItem{kind: immKindPlaylist, id: "pl1", title: "Stuff"})
	updated, _ := m.Update(immersiveArtistMsg{
		detail:       provider.ArtistDetail{Popular: []playlist.Track{{Title: "Wrong"}}},
		providerName: "stub",
		gen:          artistGen,
	})
	got := updated.(Model)
	if got.immersive.view != immViewPlaylist || len(got.immersive.tracks) != 0 {
		t.Fatalf("stale artist result applied: view=%d tracks=%v", got.immersive.view, got.immersive.tracks)
	}
}

// Switching pills drops the opened context, whatever the target section.
func TestImmersiveSectionSwitchClearsContext(t *testing.T) {
	for _, sec := range []immSection{immSecSearch, immSecAlbums} {
		m := immersiveModel(t)
		m.immersive.view = immViewPlaylist
		m.immersive.ctxName = "Stuff"
		m.immersive.tracks = []playlist.Track{{Title: "Song A"}}
		m.immersive.tracksLoading = true
		m.immersiveSetSection(sec)
		im := m.immersive
		if im.ctxName != "" || im.tracks != nil || im.tracksLoading {
			t.Fatalf("section %d kept context: name=%q tracks=%v loading=%v", sec, im.ctxName, im.tracks, im.tracksLoading)
		}
	}
}

// Esc cancels the filter; the filter is shown in the canvas title.
func TestImmersiveFilterVisibleAndCancelled(t *testing.T) {
	m := immersiveModel(t)
	m.handleImmersiveKey(keyMsg("f"))
	m.handleImmersiveKey(keyMsg("z"))
	if title := m.immCanvasTitle(); !strings.Contains(title, "filter: z") {
		t.Fatalf("canvas title %q does not show the filter", title)
	}
	m.handleImmersiveKey(keyMsg("esc"))
	if m.immersive.filter != "" || m.immersive.filtering {
		t.Fatalf("esc left filter=%q filtering=%v", m.immersive.filter, m.immersive.filtering)
	}
}

func TestImmersiveInputBackspaceIsRuneAware(t *testing.T) {
	m := immersiveModel(t)
	m.immersive.searching = true
	m.immersive.searchQuery = "café"
	m.handleImmersiveInputKey(tea.KeyPressMsg{Code: tea.KeyBackspace})
	if m.immersive.searchQuery != "caf" {
		t.Fatalf("backspace left %q, want %q", m.immersive.searchQuery, "caf")
	}
}

// The volume hit rect is where the bar is drawn.
func TestImmersiveVolumeBarHitMatchesRender(t *testing.T) {
	m := immersiveMouseModel(t)
	g := m.immGeom()
	_, vol := m.immControlsGeom(g.w)
	row := []rune(stripAnsi(m.renderImmControls(g)[1]))
	bar := string(row[vol.X : vol.X+vol.W])
	if strings.Trim(bar, "▮▯") != "" {
		t.Fatalf("volume rect covers %q, want only bar cells", bar)
	}
}

// With the full-screen visualizer up, the wheel must not move the hidden canvas.
func TestImmersiveWheelIgnoredUnderFullVis(t *testing.T) {
	m := immersiveMouseModel(t)
	m.fullVis = true
	m.handleMouseWheel(tea.MouseWheelMsg{Button: tea.MouseWheelDown})
	if m.immersive.cursor != 0 {
		t.Fatalf("wheel moved the hidden canvas cursor to %d", m.immersive.cursor)
	}
}

// At the minimum frame the queue panel shows no rows, so it has no hit rows.
func TestImmersiveQueueHitRowsMatchPanel(t *testing.T) {
	m := immersiveMouseModel(t)
	m.width, m.height = immMinWidth, immMinHeight
	m.recomputeLayout()
	m.playlist.Replace([]playlist.Track{{Title: "One"}, {Title: "Two"}, {Title: "Three"}})
	m.View()
	g := m.immGeom()
	if want := max(0, g.queueH-3); m.immMouse.queueN > want {
		t.Fatalf("queue hit rows = %d, panel shows at most %d", m.immMouse.queueN, want)
	}
}

func TestShareLinkNeverCopiesCredentials(t *testing.T) {
	tests := []struct {
		path, want string
	}{
		{"spotify:track:abc", "https://open.spotify.com/track/abc"},
		{"https://radio.example.com/live.mp3", "cliamp://play?url=https%3A%2F%2Fradio.example.com%2Flive.mp3"},
		{"https://navi.example.com/rest/stream?id=1&u=me&t=tok&s=salt", ""},
		{"https://plex.example.com/library/parts/1/file.mp3?X-Plex-Token=secret", ""},
		{"https://user:pass@host.example.com/a.mp3", ""},
		{"/music/local.flac", ""},
	}
	for _, tt := range tests {
		if got := shareLink(playlist.Track{Path: tt.path}); got != tt.want {
			t.Errorf("shareLink(%q) = %q, want %q", tt.path, got, tt.want)
		}
	}
}

// Leaving a collection while it loads, then coming forward again, reloads
// it instead of showing an empty list.
func TestImmersiveForwardReloadsUnloadedList(t *testing.T) {
	m := immersiveModel(t)
	m.openImmersiveItem(immItem{kind: immKindPlaylist, id: "pl1", title: "Stuff"})
	m.immersiveGoBack()
	cmd := m.immersiveGoForward()
	if m.immersive.view != immViewPlaylist || !m.immersive.tracksLoading || cmd == nil {
		t.Fatalf("forward: view=%d loading=%v cmd=%v, want a reload of the playlist", m.immersive.view, m.immersive.tracksLoading, cmd != nil)
	}
}

func TestImmersiveSettingsNavigationClearsForward(t *testing.T) {
	m := immersiveModel(t)
	m.immersiveSetSection(immSecAlbums)
	m.immersiveGoBack()
	m.handleImmersiveKey(keyMsg("e")) // settings tab
	m.immersiveSetSection(immSecArtists)
	if len(m.immersive.fwd) != 0 {
		t.Fatal("a new navigation from Settings must clear forward history")
	}
	m.handleImmersiveKey(keyMsg("e"))
	m.immersiveGoBack() // leaves Settings only
	if m.immersive.view == immViewSettings || len(m.immersive.fwd) != 0 {
		t.Fatalf("back from Settings: view=%d fwd=%d", m.immersive.view, len(m.immersive.fwd))
	}
}

// Go to album from the track menu opens in the immersive canvas, not in a
// classic screen hidden underneath it.
func TestImmersiveGoToAlbumOpensInCanvas(t *testing.T) {
	m := immersiveModel(t)
	updated, _ := m.Update(menuAlbumMsg{album: provider.AlbumInfo{ID: "al7", Name: "Kamikaze"}, providerName: "stub", gen: m.requests.trackMenu})
	got := updated.(Model)
	if got.immersive.view != immViewAlbum || got.immersive.ctxID != "al7" || got.navBrowser.visible {
		t.Fatalf("view=%d ctx=%q navBrowser=%v, want the album in the canvas", got.immersive.view, got.immersive.ctxID, got.navBrowser.visible)
	}
}

// Below the immersive minimum size the classic layout is the one on screen,
// so go to album takes the classic path instead of opening in the hidden
// canvas or asking the user to leave immersive.
func TestImmersiveGoToAlbumTooSmallUsesClassic(t *testing.T) {
	m := immersiveModel(t)
	m.width, m.height = immMinWidth-1, immMinHeight-1
	updated, _ := m.Update(menuAlbumMsg{album: provider.AlbumInfo{ID: "al7", Name: "Kamikaze"}, providerName: "stub", gen: m.requests.trackMenu})
	got := updated.(Model)
	if got.immersive.view == immViewAlbum || strings.Contains(got.status.text, "leave immersive") {
		t.Fatalf("view=%d status=%q, want the classic album path", got.immersive.view, got.status.text)
	}
}

// Quitting clears the layer, and the exit sequence (leave the alternate
// screen, home, erase below) is not a frame: nothing is redrawn or erased
// over the shell once the alternate screen is gone.
func TestImmersiveQuitLeavesShellAlone(t *testing.T) {
	m := immersiveModel(t)
	layer := termimg.NewLayer()
	m.SetImageLayer(layer)
	f, err := os.CreateTemp(t.TempDir(), "out")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	w := termimg.NewWriter(f, layer)
	layer.Set([]termimg.Placement{{Key: "cover", X: 3, Y: 4, W: 6, H: 2, Data: []byte("<COVER>")}})
	_, _ = w.Write([]byte("frame1"))
	m.quitting = true
	m.View()
	before, _ := os.ReadFile(f.Name())
	exit := "\x1b[?1049l\x1b[H\x1b[J"
	_, _ = w.Write([]byte(exit))
	after, _ := os.ReadFile(f.Name())
	if last := string(after[len(before):]); last != exit {
		t.Fatalf("exit sequence altered: %q", last)
	}
	// The next real frame (if any) erases the cover instead of redrawing it.
	_, _ = w.Write([]byte("text"))
	final, _ := os.ReadFile(f.Name())
	if tail := string(final[len(after):]); strings.Contains(tail, "<COVER>") || !strings.Contains(tail, "\x1b[6X") {
		t.Fatalf("frame after quit should erase, not redraw, the cover: %q", tail)
	}
}

// Right-clicking a collection selects it; the track menu is for tracks.
func TestImmersiveRightClickCollectionSelectsOnly(t *testing.T) {
	m := immersiveMouseModel(t)
	m.playlist.Replace([]playlist.Track{{Title: "Playing", Path: "/p"}})
	m.View()
	it := m.immMouse.items[1]
	immClickAt(m, it.box.X+1, it.box.Y, tea.MouseRight)
	if m.trackMenu.visible || m.immersive.cursor != 1 {
		t.Fatalf("menu visible=%v cursor=%d, want selection only", m.trackMenu.visible, m.immersive.cursor)
	}
}
