package model

import (
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/bjarneo/cliamp/playlist"
	"github.com/bjarneo/cliamp/provider"
	"github.com/bjarneo/cliamp/ui"
)

type localInlineFake struct {
	commandsTestProvider
}

func (p *localInlineFake) Tracks(string) ([]playlist.Track, error) { return nil, nil }

func newLocalInlineModel() Model {
	local := &localInlineFake{commandsTestProvider{name: "Local", lists: []playlist.PlaylistInfo{
		{ID: "music", Name: "music"},
	}}}
	remote := commandsTestProvider{name: "Spotify"}
	return Model{
		playlist:      playlist.New(),
		provider:      local,
		localProvider: local,
		providers: []provider.Entry{
			{Key: "local", Name: "Local", Provider: local},
			{Key: "spotify", Name: "Spotify", Provider: remote},
		},
		provPillIdx: 0,
		focus:       focusProvider,
		vis:         ui.NewVisualizer(48000),
	}
}

// Switching to the Local source replaces the old read-only provider pane
// list with the playlist.
func TestLocalSourceShowsManager(t *testing.T) {
	m := newLocalInlineModel()
	m.plManager.visible = false

	m.switchProvider(0)

	if !m.plManager.visible {
		t.Fatal("switching to local should open the playlist")
	}
	if m.focus != focusProvider {
		t.Fatalf("focus = %v, want focusProvider", m.focus)
	}
}

func TestNonLocalSourceKeepsPane(t *testing.T) {
	m := newLocalInlineModel()

	m.switchProvider(1)

	if m.plManager.visible {
		t.Fatal("switching to a remote provider must not open the playlist")
	}
}

func TestStartInLocalOpensManager(t *testing.T) {
	m := newLocalInlineModel()
	m.plManager.visible = false

	m.StartInProvider()

	if !m.plManager.visible {
		t.Fatal("starting in Local should open the playlist")
	}
}

func TestEscFromPlaylistOpensLocalManager(t *testing.T) {
	m := newLocalInlineModel()
	m.focus = focusPlaylist
	m.plManager.visible = false

	m.handleMainKey(tea.KeyPressMsg{Text: "esc", Code: tea.KeyEscape})

	if m.focus != focusProvider {
		t.Fatalf("focus = %v, want focusProvider", m.focus)
	}
	if !m.plManager.visible {
		t.Fatal("esc to the Local source should open the playlist")
	}
}

func TestClosingLocalManagerLandsOnQueue(t *testing.T) {
	m := newLocalInlineModel()
	m.openPlaylistManager()
	m.focus = focusProvider

	m.handlePlaylistManagerKey(tea.KeyPressMsg{Text: "esc", Code: tea.KeyEscape})

	if m.plManager.visible {
		t.Fatal("esc should close the playlist list")
	}
	if m.focus != focusPlaylist {
		t.Fatalf("focus = %v, want focusPlaylist (queue), not the removed Local pane list", m.focus)
	}
}
