package model

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/bjarneo/cliamp/playlist"
)

func TestCanceledSearchOverlayAlbumResponseIsIgnored(t *testing.T) {
	tests := []struct {
		name   string
		cancel func(*Model)
	}{
		{
			name: "close overlay",
			cancel: func(m *Model) {
				m.closeSearchOverlay()
			},
		},
		{
			name: "back to input",
			cancel: func(m *Model) {
				m.handleSearchOverlayResultsKey(tea.KeyPressMsg{Code: tea.KeyEscape})
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			canceled := make(chan struct{})
			m := Model{
				playlist: playlist.New(),
				searchOverlay: searchOverlayState{
					visible:      true,
					screen:       searchOverlayResults,
					albumLoading: true,
					cancel:       func() { close(canceled) },
				},
			}
			const gen = 7
			m.requests.searchOverlayAlbum = gen

			tt.cancel(&m)
			select {
			case <-canceled:
			default:
				t.Fatal("album request context was not canceled")
			}
			if m.requests.searchOverlayAlbum == gen {
				t.Fatal("album request generation was not invalidated")
			}

			updated, cmd := m.Update(searchOverlayAlbumTracksMsg{
				gen:    gen,
				action: searchOverlayAlbumPlay,
				album:  albumResult("Late Album"),
				tracks: []playlist.Track{{Title: "Late Track"}},
			})
			m = updated.(Model)
			if cmd != nil {
				t.Fatal("stale album response returned a command")
			}
			if m.playlist.Len() != 0 {
				t.Fatalf("playlist length = %d after stale response, want 0", m.playlist.Len())
			}
		})
	}
}

type contextAlbumLoader struct{}

func (contextAlbumLoader) AlbumTracks(string) ([]playlist.Track, error) {
	return nil, errors.New("legacy album loader called")
}

func (contextAlbumLoader) AlbumTracksContext(ctx context.Context, _ string) ([]playlist.Track, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}

func TestFetchSearchOverlayAlbumTracksCmdUsesContextLoader(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	msg := fetchSearchOverlayAlbumTracksCmd(ctx, contextAlbumLoader{}, albumResult("Album"), searchOverlayAlbumPlay, 1)().(searchOverlayAlbumTracksMsg)
	if !errors.Is(msg.err, context.Canceled) {
		t.Fatalf("album load error = %v, want context.Canceled", msg.err)
	}
}

func TestLeavingSearchOverlayResultsCancelsPlaylistLookup(t *testing.T) {
	canceled := make(chan struct{})
	m := Model{
		searchOverlay: searchOverlayState{
			visible: true,
			screen:  searchOverlayResults,
			loading: true,
			prov:    commandsTestProvider{name: "Spotify"},
			cancel:  func() { close(canceled) },
		},
	}
	const gen = 7
	m.requests.searchOverlayLists = gen

	m.handleSearchOverlayResultsKey(tea.KeyPressMsg{Code: tea.KeyEscape})
	select {
	case <-canceled:
	default:
		t.Fatal("playlist lookup context was not canceled")
	}
	if m.requests.searchOverlayLists == gen {
		t.Fatal("playlist request generation was not invalidated")
	}
	if m.searchOverlay.loading {
		t.Fatal("playlist lookup remained loading")
	}

	updated, cmd := m.Update(searchOverlayPlaylistsMsg{
		gen:          gen,
		providerName: "Spotify",
		playlists:    []playlist.PlaylistInfo{{ID: "late", Name: "Late"}},
	})
	m = updated.(Model)
	if cmd != nil {
		t.Fatal("stale playlist response returned a command")
	}
	if m.searchOverlay.screen != searchOverlayInput || m.searchOverlay.playlists != nil {
		t.Fatalf("stale playlist response changed search state: %+v", m.searchOverlay)
	}
}

// R in the search results replaces the queue with the selected album or track
// and plays its first track, where Enter adds to the queue.
func TestSearchOverlayReplaceQueue(t *testing.T) {
	tests := []struct {
		name   string
		action searchOverlayAlbumAction
		want   []string
		index  int
	}{
		{name: "replace", action: searchOverlayAlbumReplace, want: []string{"d.mp3", "e.mp3"}, index: 0},
		{name: "play keeps the queue", action: searchOverlayAlbumPlay, want: []string{"a.mp3", "b.mp3", "c.mp3", "d.mp3", "e.mp3"}, index: 3},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := newPlaybackChange()
			c.m.requests.searchOverlayAlbum = 1
			updated, _ := c.m.Update(searchOverlayAlbumTracksMsg{
				gen:    1,
				action: tt.action,
				album:  albumResult("Album"),
				tracks: []playlist.Track{{Title: "D", Path: "d.mp3"}, {Title: "E", Path: "e.mp3"}},
			})
			m := updated.(Model)
			var got []string
			for _, track := range m.playlist.Tracks() {
				got = append(got, track.Path)
			}
			if !slices.Equal(got, tt.want) {
				t.Fatalf("queue = %v, want %v", got, tt.want)
			}
			if idx := m.playlist.Index(); idx != tt.index {
				t.Fatalf("playlist index = %d, want %d", idx, tt.index)
			}
		})
	}
}

// R replaces the queue at once only when it is empty. A non-empty queue is
// confirmed first, because the replace has no undo snapshot.
func TestSearchOverlayReplaceKey(t *testing.T) {
	trackResult := playlist.Track{Title: "D", Path: "d.mp3"}
	tests := []struct {
		name        string
		result      playlist.Track
		emptyQueue  bool
		keys        []string
		want        []string
		wantPlay    string // the track that starts, or "" when none does
		wantConfirm bool
		expanding   bool
	}{
		{name: "track on empty queue", result: trackResult, emptyQueue: true, keys: []string{"R"}, want: []string{"d.mp3"}, wantPlay: "d.mp3"},
		{name: "track asks first", result: trackResult, keys: []string{"R"}, want: []string{"a.mp3", "b.mp3", "c.mp3"}, wantConfirm: true},
		{name: "track confirmed", result: trackResult, keys: []string{"R", "enter"}, want: []string{"d.mp3"}, wantPlay: "d.mp3"},
		{name: "esc cancels", result: trackResult, keys: []string{"R", "esc"}, want: []string{"a.mp3", "b.mp3", "c.mp3"}},
		{name: "R cancels", result: trackResult, keys: []string{"R", "R"}, want: []string{"a.mp3", "b.mp3", "c.mp3"}},
		{name: "other keys wait", result: trackResult, keys: []string{"R", "a", "q"}, want: []string{"a.mp3", "b.mp3", "c.mp3"}, wantConfirm: true},
		{name: "album on empty queue", result: albumResult("Album"), emptyQueue: true, keys: []string{"R"}, expanding: true},
		{name: "album asks first", result: albumResult("Album"), keys: []string{"R"}, want: []string{"a.mp3", "b.mp3", "c.mp3"}, wantConfirm: true},
		{name: "album confirmed", result: albumResult("Album"), keys: []string{"R", "enter"}, want: []string{"a.mp3", "b.mp3", "c.mp3"}, expanding: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := newPlaybackChange()
			if tt.emptyQueue {
				c.m.playlist.Replace(nil)
			}
			c.m.searchOverlay = searchOverlayState{
				visible: true,
				screen:  searchOverlayResults,
				prov:    contextAlbumLoaderProvider{},
				results: []playlist.Track{tt.result},
			}
			for _, key := range tt.keys {
				code := rune(key[0])
				if key == "enter" {
					code = tea.KeyEnter
				} else if key == "esc" {
					code = tea.KeyEscape
				}
				text := key
				if len(key) > 1 {
					text = ""
				}
				c.m.handleSearchOverlayResultsKey(tea.KeyPressMsg{Code: code, Text: text})
			}
			if c.m.searchOverlay.confirmReplace != tt.wantConfirm {
				t.Fatalf("confirmReplace = %v, want %v", c.m.searchOverlay.confirmReplace, tt.wantConfirm)
			}
			if c.m.searchOverlay.albumLoading != tt.expanding {
				t.Fatalf("albumLoading = %v, want %v", c.m.searchOverlay.albumLoading, tt.expanding)
			}
			var got []string
			for _, track := range c.m.playlist.Tracks() {
				got = append(got, track.Path)
			}
			if !slices.Equal(got, tt.want) {
				t.Fatalf("queue = %v, want %v", got, tt.want)
			}
			if tt.wantPlay == "" {
				return
			}
			if c.m.playingTrack.Path != tt.wantPlay {
				t.Fatalf("playing %q, want %q", c.m.playingTrack.Path, tt.wantPlay)
			}
			if c.m.playlist.Index() != 0 || c.m.focus != focusPlaylist {
				t.Fatalf("index = %d, focus = %v; want 0 and the playlist", c.m.playlist.Index(), c.m.focus)
			}
			if !strings.HasPrefix(c.m.status.text, "Replaced queue with") {
				t.Fatalf("status = %q", c.m.status.text)
			}
		})
	}
}

// A warning from starting the new queue is not replaced by the success line.
func TestSearchOverlayReplaceKeepsStartWarning(t *testing.T) {
	c := newPlaybackChange()
	c.m.focus = focusProvider
	c.m.requests.searchOverlayAlbum = 1
	updated, _ := c.m.Update(searchOverlayAlbumTracksMsg{
		gen:    1,
		action: searchOverlayAlbumReplace,
		album:  albumResult("Album"),
		tracks: []playlist.Track{{Title: "D", Path: "d.mp3", Unplayable: true}},
	})
	m := updated.(Model)
	if m.status.text != "No available tracks" {
		t.Fatalf("status = %q, want the start warning", m.status.text)
	}
	if m.focus != focusPlaylist {
		t.Fatalf("focus = %v, want the playlist", m.focus)
	}
}

// contextAlbumLoaderProvider is a provider that can expand albums.
type contextAlbumLoaderProvider struct {
	commandsTestProvider
	contextAlbumLoader
}
