package model

import (
	"fmt"
	"strings"
	"testing"

	"cliamp/playlist"
	"cliamp/ui"
	"github.com/charmbracelet/lipgloss"
)

func withFrameWidth(t *testing.T, width int) {
	t.Helper()
	prevFrameStyle := ui.FrameStyle
	prevPanelWidth := ui.PanelWidth
	ui.FrameStyle = ui.FrameStyle.Width(width)
	ui.PanelWidth = max(0, width-2*ui.PaddingH)
	t.Cleanup(func() {
		ui.FrameStyle = prevFrameStyle
		ui.PanelWidth = prevPanelWidth
	})
}

func TestMainViewShrinksPlaylistForFooterMessages(t *testing.T) {
	if sharedPlayer == nil {
		t.Skip("audio hardware unavailable")
	}
	withFrameWidth(t, 80)

	pl := playlist.New()
	for i := range 12 {
		pl.Add(playlist.Track{
			Path:  fmt.Sprintf("/tmp/track-%d.mp3", i),
			Title: fmt.Sprintf("Track %d", i+1),
		})
	}

	m := Model{
		player:    sharedPlayer,
		playlist:  pl,
		vis:       ui.NewVisualizer(float64(sharedPlayer.SampleRate())),
		width:     80,
		plVisible: 3,
	}
	m.vis.Mode = ui.VisNone
	m.save.startDownload()
	m.status.Show("Saved", statusTTLDefault)
	m.height = m.mainFrameFixedLines(true) + 1

	if got := m.effectivePlaylistVisible(); got != 1 {
		t.Fatalf("effectivePlaylistVisible() = %d, want 1 with one row left after footer lines", got)
	}
	if got := lipgloss.Height(m.View()); got > m.height {
		t.Fatalf("View() height = %d, want <= %d after footer lines shrink playlist", got, m.height)
	}
}

func TestRenderPlaylistKeepsCursorVisibleWhenFooterShrinksBudget(t *testing.T) {
	if sharedPlayer == nil {
		t.Skip("audio hardware unavailable")
	}
	withFrameWidth(t, 80)

	sharedPlayer.Stop()

	pl := playlist.New()
	for i := range 12 {
		pl.Add(playlist.Track{
			Path:  fmt.Sprintf("/tmp/track-%d.mp3", i),
			Title: fmt.Sprintf("Track %d", i+1),
		})
	}

	m := Model{
		player:    sharedPlayer,
		playlist:  pl,
		vis:       ui.NewVisualizer(float64(sharedPlayer.SampleRate())),
		width:     80,
		focus:     focusPlaylist,
		plVisible: 3,
		plScroll:  7,
		plCursor:  9,
	}
	m.vis.Mode = ui.VisNone
	m.save.startDownload()
	m.status.Show("Saved", statusTTLDefault)
	m.height = m.mainFrameFixedLines(true) + 2

	if got := m.effectivePlaylistVisible(); got != 2 {
		t.Fatalf("effectivePlaylistVisible() = %d, want 2 with footer-shrunk playlist", got)
	}

	out := m.renderPlaylist()
	if !strings.Contains(out, "10. Track 10") {
		t.Fatalf("renderPlaylist() = %q, want selected row to remain visible", out)
	}
}

func TestViewConsumesInitialVisualizerRefresh(t *testing.T) {
	if sharedPlayer == nil {
		t.Skip("audio hardware unavailable")
	}
	withFrameWidth(t, 80)

	m := Model{
		player:   sharedPlayer,
		playlist: playlist.New(),
		vis:      ui.NewVisualizer(float64(sharedPlayer.SampleRate())),
		width:    80,
		height:   24,
	}

	if !m.vis.RefreshPending() {
		t.Fatal("refreshPending = false on new visualizer, want initial refresh request")
	}

	_ = m.View()

	if m.vis.RefreshPending() {
		t.Fatal("refreshPending = true after first View(), want refresh consumed")
	}
	if m.vis.Frame() != 1 {
		t.Fatalf("visualizer frame after first View() = %d, want 1", m.vis.Frame())
	}
}

func TestRenderNavBrowserIncludesFooterMessages(t *testing.T) {
	withFrameWidth(t, 80)

	m := Model{
		width:  80,
		height: 24,
		navBrowser: navBrowserState{
			visible: true,
			mode:    navBrowseModeMenu,
		},
	}
	m.save.startDownload()

	if out := m.renderNavBrowser(); !strings.Contains(out, "Downloading...") {
		t.Fatalf("renderNavBrowser() missing download footer: %q", out)
	}
}
