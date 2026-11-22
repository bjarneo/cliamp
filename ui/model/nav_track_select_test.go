package model

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/bjarneo/cliamp/external/podcast"
	"github.com/bjarneo/cliamp/playlist"
	"github.com/bjarneo/cliamp/provider"
	"github.com/bjarneo/cliamp/ui"
	"github.com/charmbracelet/x/ansi"
)

func TestPodcastEpisodeListFileStyleSelection(t *testing.T) {
	t.Setenv("CLIAMP_CONFIG_DIR", t.TempDir())
	p := podcast.New("")
	episodes := []playlist.Track{
		{Title: "One", Path: "https://example.com/1.mp3"},
		{Title: "Two", Path: "https://example.com/2.mp3"},
		{Title: "Three", Path: "https://example.com/3.mp3"},
	}
	m := Model{
		provider: p, playlist: playlist.New(), player: &playbackFakeEngine{},
		vis: ui.NewVisualizer(44100),
		navBrowser: navBrowserState{
			prov: p, visible: true, screen: navBrowseScreenTracks,
			tracks: episodes, cursor: 1, selected: make(map[string]bool),
		},
	}
	m.SetVisualizer("none")

	_ = m.handleNavTrackListKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	if m.navBrowser.visible {
		t.Fatal("episode browser should close after Enter")
	}
	if m.focus != focusPlaylist {
		t.Fatalf("focus = %v, want main playlist", m.focus)
	}
	if got := m.playlist.Tracks(); len(got) != 1 || got[0].Title != "Two" {
		t.Fatalf("queue = %+v, want only the highlighted episode", got)
	}

	m.navBrowser = navBrowserState{
		prov: p, visible: true, screen: navBrowseScreenTracks,
		tracks: episodes, cursor: 0, selected: make(map[string]bool),
	}
	m.playlist = playlist.New()
	m.navBrowser.selected[episodes[0].Path] = true
	m.navBrowser.selected[episodes[2].Path] = true
	if len(m.navBrowser.selected) != 2 {
		t.Fatalf("selected = %v, want two episodes", m.navBrowser.selected)
	}
	cmd := m.handleNavTrackListKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("Enter on selection returned no command")
	}
	if tracks := m.playlist.Tracks(); len(tracks) != 2 || tracks[0].Title != "One" || tracks[1].Title != "Three" {
		t.Fatalf("queue = %+v, want [One Three]", tracks)
	}
}

func TestNavEpisodeListShowsPlaybackMarkers(t *testing.T) {
	t.Setenv("CLIAMP_CONFIG_DIR", t.TempDir())
	states := map[string]provider.PlaybackState{
		"https://example.com/1.mp3": {Played: true},
		"https://example.com/2.mp3": {Position: 5 * time.Minute},
	}
	prov := &stateProv{states: states}
	m := Model{
		layout:   frameLayout{panelWidth: 80},
		provider: prov,
		navBrowser: navBrowserState{
			prov: prov, visible: true, screen: navBrowseScreenTracks,
			tracks: []playlist.Track{
				{Title: "Done", Path: "https://example.com/1.mp3"},
				{Title: "Part", Path: "https://example.com/2.mp3"},
				{Title: "Fresh", Path: "https://example.com/3.mp3"},
			},
			selected: make(map[string]bool),
		},
	}
	body := ansi.Strip(m.renderNavTrackRowsWithSelection(10))
	if !strings.Contains(body, playedMarker) {
		t.Fatalf("body = %q, want played marker %q", body, playedMarker)
	}
	if !strings.Contains(body, partialMarker) {
		t.Fatalf("body = %q, want partial marker %q", body, partialMarker)
	}
}
