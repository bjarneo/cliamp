package model

import (
	"context"
	"errors"
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
