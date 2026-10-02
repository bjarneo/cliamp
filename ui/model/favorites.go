package model

import (
	"context"
	"errors"
	"time"

	tea "charm.land/bubbletea/v2"

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
	err      error
}

// favoriteTrackKey toggles the ♥ favorite for a track that a key selected.
// It does nothing without a favorites store, and shows an error in the status
// bar when the store fails.
func (m *Model) favoriteTrackKey(track playlist.Track) tea.Cmd {
	if m.favStore == nil {
		return nil
	}
	cmd, err := m.toggleTrackFavorite(track)
	if err != nil {
		m.status.Errorf(statusTTLDefault, "Favorite failed: %s", err)
		return nil
	}
	return cmd
}

// toggleTrackFavorite toggles the ♥ favorite for track in the local store,
// which stays the source of truth. The returned command copies the change to
// the provider that owns the track and refreshes the provider pane counts.
func (m *Model) toggleTrackFavorite(track playlist.Track) (tea.Cmd, error) {
	if m.favStore == nil {
		return nil, errFavoritesUnavailable
	}
	favorite, err := m.favStore.Toggle(track)
	if err != nil {
		return nil, err
	}
	m.refreshFavSet()
	// The Local pane renders Favorites counts from Playlists(). The
	// manager list refreshes itself on open.
	return tea.Batch(m.syncTrackFavoriteCmd(track, favorite), m.refreshPaneAfterLocalWrite()), nil
}

// findTrackFavoriter returns the first registered provider that owns track
// and keeps its own favorite state, and the name to show for it.
func (m *Model) findTrackFavoriter(track playlist.Track) (provider.TrackFavoriter, string) {
	return findCapable(m, func(f provider.TrackFavoriter) bool { return f.CanFavoriteTrack(track) })
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
		return trackFavoriteSyncedMsg{provider: name, err: err}
	}
}

// handleTrackFavoriteSynced keeps the local favorite when the provider call
// fails and tells the user which provider did not accept the change.
func (m *Model) handleTrackFavoriteSynced(msg trackFavoriteSyncedMsg) {
	if msg.err != nil {
		m.status.Warningf(statusTTLMedium, "%s did not save the favorite: %s", msg.provider, msg.err)
	}
}
