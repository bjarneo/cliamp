package model

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/bjarneo/cliamp/playlist"
)

type previewTestProvider struct {
	commandsTestProvider
	tracks     []playlist.Track
	trackErr   error
	requested  string
	resolved   string
	resolveURL bool
	started    chan struct{}
	refreshed  bool
}

func (p *previewTestProvider) Tracks(id string) ([]playlist.Track, error) {
	p.requested = id
	return p.tracks, p.trackErr
}

func (p *previewTestProvider) CanResolveURL(raw string) bool {
	return p.resolveURL && raw == "spotify:playlist:canonical"
}

func (p *previewTestProvider) ResolveURL(ctx context.Context, raw string) ([]playlist.Track, error) {
	p.resolved = raw
	if p.started != nil {
		close(p.started)
		<-ctx.Done()
		return nil, ctx.Err()
	}
	return p.tracks, p.trackErr
}

func (p *previewTestProvider) Refresh() { p.refreshed = true }

func (p *previewTestProvider) CanRefreshPlaylist(id string) bool { return id == "mix-id" }

func TestProviderPlaylistPreviewUsesAuthenticatedResolverAndKeepsPlayback(t *testing.T) {
	m := keybindingTestModel()
	m.layout.panelWidth, m.plVisible = 80, 10
	source := &previewTestProvider{
		commandsTestProvider: commandsTestProvider{name: "Spotify"},
		tracks:               []playlist.Track{{Path: "spotify:track:a", Title: "A"}, {Path: "spotify:track:b", Title: "B"}},
		resolveURL:           true,
	}
	m.provider = source
	m.activeProviderPlaylistID = "previous-id"
	m.focus = focusProvider
	m.provPane.lists = []playlist.PlaylistInfo{{ID: "spotify:playlist:canonical", Name: "Daily Mix"}}
	m.playlist.Add(playlist.Track{Path: "/old.flac", Title: "Current"})
	engine := m.player.(*playbackFakeEngine)
	engine.playing, engine.hasPreload = true, true
	queue := m.playlist.Snapshot()
	stream := m.requests.stream

	cmd := m.handleKey(tea.KeyPressMsg{Text: "v", Code: 'v'})
	if cmd == nil || !m.navBrowser.visible || !m.navBrowser.loading {
		t.Fatal("v did not open a loading preview")
	}
	if m.activeProviderPlaylistID != "previous-id" {
		t.Fatalf("opening preview changed active playlist ID to %q", m.activeProviderPlaylistID)
	}
	m.handlePlaylistPreviewLoaded(cmd().(playlistPreviewLoadedMsg))
	if source.resolved != "spotify:playlist:canonical" || source.requested != "" {
		t.Fatalf("resolver = %q, Tracks request = %q", source.resolved, source.requested)
	}
	if !strings.Contains(m.navHeaderLine(), "Preview: Daily Mix") || !strings.Contains(m.renderNavBody(), "A") {
		t.Fatalf("preview does not show its label and tracks: header=%q body=%q state=%+v", m.navHeaderLine(), m.renderNavBody(), m.navBrowser)
	}
	if !reflect.DeepEqual(queue, m.playlist.Snapshot()) || stream != m.requests.stream || engine.stopCalls != 0 || len(engine.playCalls) != 0 || !engine.hasPreload {
		t.Fatal("opening preview changed playback or the live queue")
	}
	for _, key := range []string{"a", "R", "f", "space", ">"} {
		if cmd := m.handleKey(tea.KeyPressMsg{Text: key}); cmd != nil {
			t.Fatalf("preview accepted action %q", key)
		}
	}
	if !reflect.DeepEqual(queue, m.playlist.Snapshot()) || engine.stopCalls != 0 || len(engine.playCalls) != 0 {
		t.Fatal("non-Enter preview key changed playback or the queue")
	}
}

func TestProviderPlaylistPreviewEnterStartsFromSelectedTrack(t *testing.T) {
	m := keybindingTestModel()
	m.layout.panelWidth, m.plVisible = 80, 10
	tracks := []playlist.Track{{Path: "a", Title: "A"}, {Path: "b", Title: "B"}, {Path: "c", Title: "C"}}
	source := &previewTestProvider{commandsTestProvider: commandsTestProvider{name: "Spotify"}, tracks: tracks}
	m.provider = source
	m.activeProviderPlaylistID = "previous-id"
	m.focus = focusProvider
	m.provPane.lists = []playlist.PlaylistInfo{{ID: "mix-id", Name: "Mix"}}
	m.playlist.Add(playlist.Track{Path: "old-a", Title: "Old A"})
	m.playlist.Add(playlist.Track{Path: "old-b", Title: "Old B"})
	m.playlist.SetIndex(1)
	m.player.(*playbackFakeEngine).playing = true
	cmd := m.handleKey(tea.KeyPressMsg{Text: "v", Code: 'v'})
	m.handlePlaylistPreviewLoaded(cmd().(playlistPreviewLoadedMsg))
	if m.activeProviderPlaylistID != "previous-id" {
		t.Fatalf("browsing preview changed active playlist ID to %q", m.activeProviderPlaylistID)
	}
	m.navBrowser.cursor = 1
	cmd = m.handleKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("Enter did not start preview")
	}
	cmd()
	if m.navBrowser.visible || m.playlist.Len() != 3 || m.playlist.Index() != 1 || m.playlist.Tracks()[m.playlist.Index()].Title != "B" {
		t.Fatalf("started queue = %+v, index = %d, visible = %v", m.playlist.Tracks(), m.playlist.Index(), m.navBrowser.visible)
	}
	if got := m.playlist.Tracks(); got[0].Title != "A" || got[1].Title != "B" || got[2].Title != "C" {
		t.Fatalf("replacement queue = %+v, want [A B C]", got)
	}
	if got := m.player.(*playbackFakeEngine).playCalls; len(got) != 1 || got[0] != "b" {
		t.Fatalf("play calls = %v, want selected track b", got)
	}
	if m.activeProviderPlaylistID != "mix-id" {
		t.Fatalf("active playlist ID = %q, want mix-id", m.activeProviderPlaylistID)
	}
	refresh := m.refreshActiveProvider(true)
	if refresh == nil {
		t.Fatal("refresh did not reload the active preview playlist")
	}
	refresh()
	if !source.refreshed || source.requested != "mix-id" {
		t.Fatalf("refresh = %v, requested playlist = %q, want mix-id", source.refreshed, source.requested)
	}
	cmd = m.handleKey(tea.KeyPressMsg{Text: "<", Code: '<'})
	if cmd == nil {
		t.Fatal("previous did not return the full preview playlist")
	}
	cmd()
	if m.playlist.Index() != 0 || m.playlist.Tracks()[m.playlist.Index()].Title != "A" {
		t.Fatalf("previous track = %q at %d, want A at 0", m.playlist.Tracks()[m.playlist.Index()].Title, m.playlist.Index())
	}
}

func TestProviderPlaylistPreviewFallbackErrorEmptyAndStaleResult(t *testing.T) {
	m := keybindingTestModel()
	m.layout.panelWidth, m.plVisible = 80, 10
	source := &previewTestProvider{
		commandsTestProvider: commandsTestProvider{name: "Generic"},
		tracks:               []playlist.Track{{Title: "Snapshot"}},
	}
	m.provider = source
	m.focus = focusProvider
	m.provPane.lists = []playlist.PlaylistInfo{{ID: "library-id", Name: "Library"}}
	cmd := m.handleKey(tea.KeyPressMsg{Text: "v", Code: 'v'})
	stale := cmd()
	if source.requested != "library-id" {
		t.Fatalf("Tracks requested %q, want library-id", source.requested)
	}
	m.handleKey(tea.KeyPressMsg{Code: tea.KeyEscape})
	updated, _ := m.Update(stale)
	m = updated.(Model)
	if m.navBrowser.visible || len(m.navBrowser.tracks) != 0 {
		t.Fatal("closed preview accepted a stale result")
	}

	source.trackErr = errors.New("permission denied")
	cmd = m.previewProviderPlaylist()
	m.handlePlaylistPreviewLoaded(cmd().(playlistPreviewLoadedMsg))
	if !strings.Contains(m.navBrowser.previewErr, "permission denied") || !strings.Contains(m.renderNavBody(), "partially loaded") {
		t.Fatal("preview error was not displayed")
	}
	queue := m.playlist.Snapshot()
	engine := m.player.(*playbackFakeEngine)
	if cmd := m.handleKey(tea.KeyPressMsg{Code: tea.KeyEnter}); cmd != nil {
		t.Fatal("Enter started a failed preview")
	}
	if !reflect.DeepEqual(queue, m.playlist.Snapshot()) || len(engine.playCalls) != 0 || engine.stopCalls != 0 {
		t.Fatal("failed preview changed playback or queue")
	}

	source.trackErr = nil
	source.tracks = nil
	cmd = m.previewProviderPlaylist()
	m.handlePlaylistPreviewLoaded(cmd().(playlistPreviewLoadedMsg))
	if !strings.Contains(m.renderNavBody(), "No tracks found") || m.handleKey(tea.KeyPressMsg{Code: tea.KeyEnter}) != nil {
		t.Fatal("empty preview did not remain inert")
	}
}

func TestProviderPlaylistPreviewCloseCancelsResolverAndDropsResult(t *testing.T) {
	m := keybindingTestModel()
	source := &previewTestProvider{
		commandsTestProvider: commandsTestProvider{name: "Spotify"},
		resolveURL:           true,
		started:              make(chan struct{}),
	}
	m.provider = source
	m.focus = focusProvider
	m.provPane.lists = []playlist.PlaylistInfo{{ID: "spotify:playlist:canonical", Name: "Daily Mix"}}
	before := m.playlist.Snapshot()
	cmd := m.handleKey(tea.KeyPressMsg{Text: "v", Code: 'v'})
	result := make(chan tea.Msg, 1)
	go func() { result <- cmd() }()
	<-source.started
	m.handleKey(tea.KeyPressMsg{Code: tea.KeyEscape})
	msg := <-result
	if msg.(playlistPreviewLoadedMsg).err == nil {
		t.Fatal("closing preview did not cancel its resolver context")
	}
	updated, _ := m.Update(msg)
	m = updated.(Model)
	if m.navBrowser.visible || len(m.navBrowser.tracks) != 0 || !reflect.DeepEqual(before, m.playlist.Snapshot()) {
		t.Fatal("canceled stale result changed preview or queue")
	}
}

func TestProviderPlaylistPreviewSwitchDropsGenericTracksResult(t *testing.T) {
	m := keybindingTestModel()
	source := &previewTestProvider{
		commandsTestProvider: commandsTestProvider{name: "Generic"},
		tracks:               []playlist.Track{{Title: "Late result"}},
	}
	m.provider = source
	m.focus = focusProvider
	m.provPane.lists = []playlist.PlaylistInfo{{ID: "library-id", Name: "Library"}}
	cmd := m.handleKey(tea.KeyPressMsg{Text: "v", Code: 'v'})
	stale := cmd()
	switchKey := tea.KeyPressMsg{Code: 'y', ShiftedCode: 'Y', Mod: tea.ModShift}
	m.handleKey(switchKey)
	updated, _ := m.Update(stale)
	m = updated.(Model)
	if m.provider.Name() != "YouTube" || m.navBrowser.visible || len(m.navBrowser.tracks) != 0 {
		t.Fatal("provider switch accepted the old preview result")
	}
}

func TestProviderPlaylistPreviewKeepsPartialResultsButCannotStart(t *testing.T) {
	m := keybindingTestModel()
	m.layout.panelWidth, m.plVisible = 80, 10
	source := &previewTestProvider{
		commandsTestProvider: commandsTestProvider{name: "Spotify"},
		tracks:               []playlist.Track{{Title: "Available"}},
		trackErr:             errors.New("one page failed"),
		resolveURL:           true,
	}
	m.provider = source
	m.focus = focusProvider
	m.provPane.lists = []playlist.PlaylistInfo{{ID: "spotify:playlist:canonical", Name: "Daily Mix"}}
	cmd := m.handleKey(tea.KeyPressMsg{Text: "v", Code: 'v'})
	if cmd == nil || m.handleKey(tea.KeyPressMsg{Code: tea.KeyEnter}) != nil {
		t.Fatal("loading preview accepted Enter")
	}
	m.handlePlaylistPreviewLoaded(cmd().(playlistPreviewLoadedMsg))
	if len(m.navBrowser.tracks) != 1 || !strings.Contains(m.renderNavBody(), "partially loaded") || !strings.Contains(m.renderNavBody(), "Available") {
		t.Fatal("partial tracks and error were not both shown")
	}
	if m.handleKey(tea.KeyPressMsg{Code: tea.KeyEnter}) != nil {
		t.Fatal("Enter started a partial error result")
	}
}
