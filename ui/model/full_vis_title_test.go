package model

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/bjarneo/cliamp/playlist"
	"github.com/bjarneo/cliamp/ui"
)

func fullVisModel(t *testing.T) *Model {
	t.Helper()
	old := ui.PanelWidth
	ui.PanelWidth = 80
	t.Cleanup(func() { ui.PanelWidth = old })

	m := &Model{
		playlist: playlist.New(),
		provider: &plainProv{},
		fullVis:  true,
	}
	m.playlist.Replace([]playlist.Track{{
		Path:  "https://cdn/ep.mp3",
		Title: "Netanyahu Knew",
		Album: "Part Of The Problem",
	}})
	m.playlist.SetIndex(0)
	return m
}

func TestFullVisTopLineNamesTheTrackByDefault(t *testing.T) {
	m := fullVisModel(t)

	got := stripAnsi(m.fullVisTopLine())

	if !strings.Contains(got, "Netanyahu Knew") {
		t.Errorf("top line = %q, want the episode name", got)
	}
}

func TestFullVisTopLineHidesTheTrack(t *testing.T) {
	m := fullVisModel(t)
	m.hideTrackInfo = true

	got := stripAnsi(m.fullVisTopLine())

	if strings.Contains(got, "Netanyahu Knew") {
		t.Errorf("top line = %q, want the episode name gone", got)
	}
	if want := "[Plain]"; !strings.Contains(got, want) {
		t.Errorf("top line = %q, want the bracketed source %q", got, want)
	}
}

func TestFullVisTopLineWithoutAProvider(t *testing.T) {
	m := fullVisModel(t)
	m.provider = nil
	m.hideTrackInfo = true

	if got := stripAnsi(m.fullVisTopLine()); !strings.Contains(got, "[Playing]") {
		t.Errorf("top line = %q, want a fallback label", got)
	}
}

// t toggles the title inside the full-screen visualizer. In the normal view the
// same key opens the theme picker, and the two handlers must not cross.
func TestFullVisTKeyTogglesTheTitle(t *testing.T) {
	m := fullVisModel(t)
	key := tea.KeyPressMsg{Code: 't'}

	m.handleKey(key)

	if !m.hideTrackInfo {
		t.Error("the first t did not hide the track")
	}
	if m.themePicker.visible {
		t.Error("t opened the theme picker inside the full-screen visualizer")
	}

	m.handleKey(key)

	if m.hideTrackInfo {
		t.Error("the second t did not bring the track back")
	}
}

func TestTKeyStillOpensTheThemePickerOutsideFullVis(t *testing.T) {
	m := fullVisModel(t)
	m.fullVis = false
	m.themes = nil

	m.handleKey(tea.KeyPressMsg{Code: 't'})

	if m.hideTrackInfo {
		t.Error("t hid the track outside the full-screen visualizer")
	}
	if !m.themePicker.visible {
		t.Error("t no longer opens the theme picker in the normal view")
	}
}

// The label names the provider the track came from, not whichever one the
// listener has browsed to since.
func TestFullVisTopLineKeepsThePlayingProvider(t *testing.T) {
	m := fullVisModel(t)
	m.hideTrackInfo = true
	m.playingProvider = "Podcasts"
	m.provider = &plainProv{} // switched to another provider while it plays

	if got := stripAnsi(m.fullVisTopLine()); !strings.Contains(got, "[Podcasts]") {
		t.Errorf("top line = %q, want the playing track's provider", got)
	}
}

func TestClearPlaybackTrackForgetsTheProvider(t *testing.T) {
	m := fullVisModel(t)
	m.playingProvider = "Podcasts"

	m.clearPlaybackTrack()

	if m.playingProvider != "" {
		t.Errorf("playingProvider = %q after clearing, want empty", m.playingProvider)
	}
}
