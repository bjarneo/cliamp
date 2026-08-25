package model

import (
	"context"
	"errors"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/bjarneo/cliamp/playlist"
)

func TestCanceledSpotAlbumResponseIsIgnored(t *testing.T) {
	tests := []struct {
		name   string
		cancel func(*Model)
	}{
		{
			name: "close overlay",
			cancel: func(m *Model) {
				m.closeSpotSearch()
			},
		},
		{
			name: "back to input",
			cancel: func(m *Model) {
				m.handleSpotSearchResultsKey(tea.KeyPressMsg{Code: tea.KeyEscape})
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			canceled := make(chan struct{})
			m := Model{
				playlist: playlist.New(),
				spotSearch: spotSearchState{
					visible:      true,
					screen:       spotSearchResults,
					albumLoading: true,
					cancel:       func() { close(canceled) },
				},
			}
			const gen = 7
			m.requests.spotAlbum = gen

			tt.cancel(&m)
			select {
			case <-canceled:
			default:
				t.Fatal("album request context was not canceled")
			}
			if m.requests.spotAlbum == gen {
				t.Fatal("album request generation was not invalidated")
			}

			updated, cmd := m.Update(spotAlbumTracksMsg{
				gen:    gen,
				action: spotAlbumPlay,
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

func TestFetchSpotAlbumTracksCmdUsesContextLoader(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	msg := fetchSpotAlbumTracksCmd(ctx, contextAlbumLoader{}, albumResult("Album"), spotAlbumPlay, 1)().(spotAlbumTracksMsg)
	if !errors.Is(msg.err, context.Canceled) {
		t.Fatalf("album load error = %v, want context.Canceled", msg.err)
	}
}

func TestLeavingSpotResultsCancelsPlaylistLookup(t *testing.T) {
	canceled := make(chan struct{})
	m := Model{
		spotSearch: spotSearchState{
			visible: true,
			screen:  spotSearchResults,
			loading: true,
			prov:    commandsTestProvider{name: "Spotify"},
			cancel:  func() { close(canceled) },
		},
	}
	const gen = 7
	m.requests.spotLists = gen

	m.handleSpotSearchResultsKey(tea.KeyPressMsg{Code: tea.KeyEscape})
	select {
	case <-canceled:
	default:
		t.Fatal("playlist lookup context was not canceled")
	}
	if m.requests.spotLists == gen {
		t.Fatal("playlist request generation was not invalidated")
	}
	if m.spotSearch.loading {
		t.Fatal("playlist lookup remained loading")
	}

	updated, cmd := m.Update(spotPlaylistsMsg{
		gen:          gen,
		providerName: "Spotify",
		playlists:    []playlist.PlaylistInfo{{ID: "late", Name: "Late"}},
	})
	m = updated.(Model)
	if cmd != nil {
		t.Fatal("stale playlist response returned a command")
	}
	if m.spotSearch.screen != spotSearchInput || m.spotSearch.playlists != nil {
		t.Fatalf("stale playlist response changed search state: %+v", m.spotSearch)
	}
}
