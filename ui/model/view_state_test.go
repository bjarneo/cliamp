package model

import (
	"fmt"
	"regexp"
	"strings"
	"testing"

	"charm.land/lipgloss/v2"

	"cliamp/playlist"
	"cliamp/ui"
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
	if got := lipgloss.Height(m.View().Content); got > m.height {
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
	if !strings.Contains(out, "Track 10") {
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

func TestOverlayViewIncludesFooterMessages(t *testing.T) {
	if sharedPlayer == nil {
		t.Skip("audio hardware unavailable")
	}
	withFrameWidth(t, 80)
	sharedPlayer.Stop()

	// Footer/transient messages are now rendered by the inline overlay layout
	// (mainSectionsOverlay) rather than by each overlay renderer.
	m := Model{
		player:    sharedPlayer,
		playlist:  playlist.New(),
		vis:       ui.NewVisualizer(float64(sharedPlayer.SampleRate())),
		width:     80,
		height:    24,
		plVisible: 5,
	}
	m.vis.Mode = ui.VisNone
	m.refreshChrome()
	m.applyHeightMode()
	m.save.startDownload()

	out := m.View().Content
	if !strings.Contains(out, "Downloading...") {
		t.Fatalf("overlay view missing download footer: %q", out)
	}
}

func TestKeymapRendersInline(t *testing.T) {
	if sharedPlayer == nil {
		t.Skip("audio hardware unavailable")
	}
	withFrameWidth(t, 80)
	sharedPlayer.Stop()

	m := Model{
		player:    sharedPlayer,
		playlist:  playlist.New(),
		vis:       ui.NewVisualizer(float64(sharedPlayer.SampleRate())),
		width:     80,
		height:    24,
		plVisible: 5,
		keymap: keymapOverlay{
			visible: true,
			entries: []keymapEntry{{key: "Space", action: "Play/Pause"}},
		},
	}
	m.vis.Mode = ui.VisNone

	out := m.View().Content
	if got := lipgloss.Height(out); got > m.height {
		t.Fatalf("View() height = %d, want <= %d for inline keymap", got, m.height)
	}
	// The keymap renders in the playlist region, so its entries appear inline
	// rather than as a standalone centered overlay.
	if !strings.Contains(out, "Play/Pause") {
		t.Fatalf("View() missing inline keymap entry: %q", out)
	}
}

func TestFullVisualizerViewFitsTerminalWidth(t *testing.T) {
	if sharedPlayer == nil {
		t.Skip("audio hardware unavailable")
	}
	withFrameWidth(t, 80)

	sharedPlayer.Stop()

	m := Model{
		player:   sharedPlayer,
		playlist: playlist.New(),
		vis:      ui.NewVisualizer(float64(sharedPlayer.SampleRate())),
		width:    80,
		height:   24,
		fullVis:  true,
	}
	m.vis.Mode = ui.VisNone

	if got := lipgloss.Width(m.View().Content); got > m.width {
		t.Fatalf("View() width = %d, want <= %d in full visualizer mode", got, m.width)
	}
}

var stripAnsiRegExp = regexp.MustCompile(`\x1b\[[0-9;]*[mK]`)

func stripAnsi(str string) string {
	return stripAnsiRegExp.ReplaceAllString(str, "")
}

func TestRenderPlaylistAddsPaddingToTrackNumber(t *testing.T) {
	if sharedPlayer == nil {
		t.Skip("audio hardware unavailable")
	}
	withFrameWidth(t, 80)

	sharedPlayer.Stop()

	pl := playlist.New()
	for i := range 120 {
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
		plVisible: 120,
	}
	m.vis.Mode = ui.VisNone
	m.height = m.mainFrameFixedLines(false) + 120

	out := m.renderPlaylist()
	lines := strings.Split(out, "\n")

	if len(lines) < 120 {
		t.Fatalf("renderPlaylist() returned %d lines, want 120", len(lines))
	}

	line9 := stripAnsi(lines[8])
	line99 := stripAnsi(lines[98])
	line119 := stripAnsi(lines[118])

	ninthLineTrackIndex := strings.Index(line9, "Track")
	ninetyNinthLineTrackIndex := strings.Index(line99, "Track")
	oneHundredNineteenthLineTrackIndex := strings.Index(line119, "Track")

	if ninthLineTrackIndex != ninetyNinthLineTrackIndex || ninthLineTrackIndex != oneHundredNineteenthLineTrackIndex {
		t.Errorf(`Track name alignment is off for 3-digit numbers.
Line 9: %q (index %d)
Line 99: %q (index %d)
Line 119: %q (index %d)`, line9, ninthLineTrackIndex, line99, ninetyNinthLineTrackIndex, line119, oneHundredNineteenthLineTrackIndex)
	}
}
