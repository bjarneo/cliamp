package model

import (
	"fmt"
	"strings"
	"testing"

	"charm.land/lipgloss/v2"

	"cliamp/playlist"
	"cliamp/ui"
)

// newInlineOverlayModel builds a Model with a real player/playlist/visualizer
// for exercising the inline overlay render path.
func newInlineOverlayModel(t *testing.T, w, h int) Model {
	t.Helper()
	sharedPlayer.Stop()

	pl := playlist.New()
	for i := range 8 {
		pl.Add(playlist.Track{
			Path:  fmt.Sprintf("/tmp/track-%d.mp3", i),
			Title: fmt.Sprintf("Track %d", i+1),
		})
	}

	m := Model{
		player:    sharedPlayer,
		playlist:  pl,
		vis:       ui.NewVisualizer(float64(sharedPlayer.SampleRate())),
		width:     w,
		height:    h,
		focus:     focusPlaylist,
		plVisible: 6,
	}
	m.vis.Mode = ui.VisNone
	m.refreshChrome()
	m.applyHeightMode()
	return m
}

// TestInlineOverlaysFitTerminal opens each overlay inline and asserts the framed
// view never overflows the terminal height or width. Overlays render in the
// playlist region beneath the live now-playing/visualizer/controls chrome, so
// the total frame must still fit.
func TestInlineOverlaysFitTerminal(t *testing.T) {
	if sharedPlayer == nil {
		t.Skip("audio hardware unavailable")
	}

	sizes := []struct{ w, h int }{
		{80, 24},
		{80, 20},
		{100, 30},
	}

	overlays := []struct {
		name string
		set  func(m *Model)
	}{
		{"themePicker", func(m *Model) { m.themePicker.visible = true }},
		{"devicePicker", func(m *Model) { m.devicePicker.visible = true }},
		{"queue", func(m *Model) { m.queue.visible = true }},
		{"info", func(m *Model) { m.showInfo = true }},
		{"search", func(m *Model) { m.search.active = true }},
		{"keymap", func(m *Model) { m.keymap.visible = true; m.keymap.entries = m.buildKeymapEntries() }},
		{"netSearch", func(m *Model) { m.netSearch.active = true }},
		{"urlInput", func(m *Model) { m.urlInputting = true }},
		{"lyrics", func(m *Model) { m.lyrics.visible = true }},
		{"jump", func(m *Model) { m.jumping = true }},
		{"spotSearch", func(m *Model) { m.spotSearch.visible = true }},
		{"navBrowser", func(m *Model) { m.navBrowser.visible = true; m.navBrowser.mode = navBrowseModeMenu }},
		{"playlistManager", func(m *Model) { m.plManager.visible = true; m.plManager.screen = plMgrScreenList }},
		{"fileBrowser", func(m *Model) { m.fileBrowser.visible = true }},
		{"visPicker", func(m *Model) { m.openVisPicker() }},
	}

	for _, sz := range sizes {
		for _, ov := range overlays {
			t.Run(fmt.Sprintf("%s_%dx%d", ov.name, sz.w, sz.h), func(t *testing.T) {
				withFrameWidth(t, sz.w)
				m := newInlineOverlayModel(t, sz.w, sz.h)
				ov.set(&m)

				out := m.View().Content
				if got := lipgloss.Height(out); got > sz.h {
					t.Fatalf("%s view height = %d, want <= %d", ov.name, got, sz.h)
				}
				for _, line := range strings.Split(out, "\n") {
					if got := lipgloss.Width(line); got > sz.w {
						t.Fatalf("%s view line width = %d, want <= %d: %q", ov.name, got, sz.w, line)
					}
				}
			})
		}
	}
}
