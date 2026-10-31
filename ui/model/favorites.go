package model

import (
	"context"
	"errors"
	"maps"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/bjarneo/cliamp/applog"
	"github.com/bjarneo/cliamp/favorites"
	"github.com/bjarneo/cliamp/playlist"
	"github.com/bjarneo/cliamp/provider"
)

// trackFavoriteSyncTimeout limits one provider favorite call.
const trackFavoriteSyncTimeout = 30 * time.Second

// errFavoritesUnavailable is returned when no local favorites store exists.
var errFavoritesUnavailable = errors.New("favorites are not available")

// trackFavoriteSyncedMsg reports the result of a provider favorite sync.
type trackFavoriteSyncedMsg struct {
	provider string
	path     string
	favorite bool
	err      error
}

// favoriteTrackKey toggles the ♥ favorite for a track that a key selected.
// It does nothing without a favorites store unless the track's provider owns
// the favorite state, and shows an error in the status bar when a save fails.
func (m *Model) favoriteTrackKey(track playlist.Track) tea.Cmd {
	if _, providerOwned := m.providerTrackFavoriteState(track); m.favStore == nil && !providerOwned {
		return nil
	}
	cmd, err := m.toggleTrackFavorite(track)
	if err != nil {
		m.status.Errorf(statusTTLDefault, "Favorite failed: %s", err)
		return nil
	}
	return cmd
}

// toggleTrackFavorite toggles the displayed ♥ state. A provider that exposes
// authoritative state is changed without reading or writing the local store.
// Other providers receive a copy of the local favorite change.
func (m *Model) toggleTrackFavorite(track playlist.Track) (tea.Cmd, error) {
	if current, providerOwned := m.providerTrackFavoriteState(track); providerOwned {
		favorite := !current
		cmd := m.syncTrackFavoriteCmd(track, favorite)
		if cmd == nil {
			return nil, errFavoritesUnavailable
		}
		pending := maps.Clone(m.pendingTrackFavorites)
		if pending == nil {
			pending = make(map[string]bool)
		}
		pending[track.Path] = favorite
		m.pendingTrackFavorites = pending
		m.showTrackFavoriteStatus(track, favorite)
		return cmd, nil
	}
	if m.favStore == nil {
		return nil, errFavoritesUnavailable
	}
	favorite := !m.playlistTrackFavorited(track)
	_, localFavorite := m.favSet[track.Path]
	var refresh tea.Cmd
	if localFavorite != favorite {
		if _, err := m.favStore.Toggle(track); err != nil {
			return nil, err
		}
		m.refreshFavSet()
		// The Local pane renders Favorites counts from Playlists(). The
		// manager list refreshes itself on open.
		refresh = m.refreshPaneAfterLocalWrite()
	}
	m.showTrackFavoriteStatus(track, favorite)
	return tea.Batch(m.syncTrackFavoriteCmd(track, favorite), refresh), nil
}

func (m *Model) showTrackFavoriteStatus(track playlist.Track, favorite bool) {
	if favorite {
		m.status.Showf(statusTTLMedium, "Favorited: %s", track.DisplayName())
	} else {
		m.status.Showf(statusTTLMedium, "Unfavorited: %s", track.DisplayName())
	}
}

// findTrackFavoriter returns the first registered provider that owns track
// and keeps its own favorite state, and the name to show for it.
func (m *Model) findTrackFavoriter(track playlist.Track) (provider.TrackFavoriter, string) {
	return findCapable(m, func(f provider.TrackFavoriter) bool { return f.CanFavoriteTrack(track) })
}

func (m *Model) providerTrackFavoriteState(track playlist.Track) (favorite, providerOwned bool) {
	state, _ := findCapable(m, func(s provider.TrackFavoriteState) bool { return s.CanFavoriteTrack(track) })
	if state == nil {
		return false, false
	}
	if favorite, ok := m.pendingTrackFavorites[track.Path]; ok {
		return favorite, true
	}
	// The provider owns the state even when this particular track has not
	// been fetched in this process. Treat an unknown state as clear instead
	// of falling back to a stale entry in favorites.toml.
	favorite, _ = state.FavoriteState(track)
	return favorite, true
}

// syncTrackFavoriteCmd copies a favorite change to the provider that owns
// track. It returns nil when no provider keeps its own favorite state. The
// network call runs in the command, never in Update. The sync queue keeps
// the calls in order, so a fast second toggle cannot finish first.
func (m *Model) syncTrackFavoriteCmd(track playlist.Track, favorite bool) tea.Cmd {
	fav, name := m.findTrackFavoriter(track)
	if fav == nil {
		return nil
	}
	if m.favSync == nil {
		m.favSync = &favorites.SyncQueue{}
	}
	run := m.favSync.Enqueue(track.Path, func() error {
		ctx, cancel := context.WithTimeout(context.Background(), trackFavoriteSyncTimeout)
		defer cancel()
		return fav.SetTrackFavorite(ctx, track, favorite)
	})
	return func() tea.Msg {
		ran, err := run()
		if !ran {
			return nil
		}
		return trackFavoriteSyncedMsg{provider: name, path: track.Path, favorite: favorite, err: err}
	}
}

// trackFavoritesLoadedMsg reports that the providers fetched the favorite
// state of the start playlist. The redraw after it shows the hearts.
type trackFavoritesLoadedMsg struct{}

// loadTrackFavoritesCmd fetches the favorite state that tracks restored from
// the last session lack, from each provider that keeps it with its listings.
// It returns nil when no provider does.
func (m *Model) loadTrackFavoritesCmd(tracks []playlist.Track) tea.Cmd {
	if len(tracks) == 0 {
		return nil
	}
	// The active provider is also in m.providers. A second call finds
	// every state known and sends no request.
	var loaders []provider.TrackFavoriteLoader
	if l, ok := m.provider.(provider.TrackFavoriteLoader); ok {
		loaders = append(loaders, l)
	}
	for _, pe := range m.providers {
		if l, ok := pe.Provider.(provider.TrackFavoriteLoader); ok {
			loaders = append(loaders, l)
		}
	}
	if len(loaders) == 0 {
		return nil
	}
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), trackFavoriteSyncTimeout)
		defer cancel()
		for _, l := range loaders {
			if err := l.LoadFavoriteStates(ctx, tracks); err != nil {
				applog.Warn("load favorites: %v", err)
			}
		}
		return trackFavoritesLoadedMsg{}
	}
}

// handleTrackFavoriteSynced drops the optimistic provider state after the
// provider cache has been updated, or rolls it back after a failure. Local
// favorites copied to Spotify and Navidrome are unaffected.
func (m *Model) handleTrackFavoriteSynced(msg trackFavoriteSyncedMsg) {
	if pending, ok := m.pendingTrackFavorites[msg.path]; ok && pending == msg.favorite {
		next := maps.Clone(m.pendingTrackFavorites)
		delete(next, msg.path)
		m.pendingTrackFavorites = next
	}
	if msg.err != nil {
		m.status.Warningf(statusTTLMedium, "%s did not save the favorite: %s", msg.provider, msg.err)
	}
}
