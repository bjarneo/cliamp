package model

import (
	tea "charm.land/bubbletea/v2"

	"github.com/bjarneo/cliamp/external/radio"
	"github.com/bjarneo/cliamp/playlist"
	"github.com/bjarneo/cliamp/provider"
)

// maybeLoadCatalogBatch triggers a catalog batch fetch when the cursor is near the
// bottom of the provider list and more entries are available.
func (m *Model) maybeLoadCatalogBatch() tea.Cmd {
	loader, ok := m.provider.(provider.CatalogLoader)
	if !ok {
		return nil
	}
	if m.catalogBatch.loading || m.catalogBatch.done || m.provSearch.active || m.provSearch.loading {
		return nil
	}
	if cs, ok := m.provider.(provider.CatalogSearcher); ok && cs.IsSearching() {
		return nil
	}
	if m.provCursor >= len(m.providerLists)-10 {
		m.catalogBatch.loading = true
		return m.fetchCatalogBatch(loader)
	}
	return nil
}

// answerLocationPrompt records the listener's answer to the location question
// and refreshes the pane, where the offer row is replaced by their country.
func (m *Model) answerLocationPrompt(allowed bool) tea.Cmd {
	m.provAskLoc = false
	consenter, ok := m.provider.(provider.LocationConsenter)
	if !ok {
		return nil
	}

	place, err := consenter.SetLocationConsent(allowed)
	if err != nil {
		// The answer holds for this run even when it could not be written.
		m.status.Errorf(statusTTLDefault, "Could not save the choice: %s", err)
	}
	switch {
	case !allowed:
		m.status.Show("Location off. Pin countries with f in Countries.", statusTTLLong)
	case place == "":
		m.status.Warning("Could not tell which country you are in. Pin countries with f in Countries.", statusTTLLong)
	default:
		m.status.Showf(statusTTLMedium, "Nearby radio: %s", place)
	}

	// The offer row is gone and, on a yes, a country row has taken its place.
	m.provLoading = true
	return m.fetchProviderPlaylists()
}

// toggleProviderFavorite toggles favorite status for the current entry in the
// provider list when the provider supports it.
func (m *Model) toggleProviderFavorite() tea.Cmd {
	if m.provLoading || m.provCursor < 0 || m.provCursor >= len(m.providerLists) || m.selectedProviderListIsBrowseEntry() {
		return nil
	}
	id := m.providerLists[m.provCursor].ID
	if sl, ok := m.provider.(provider.SectionedList); ok {
		if !sl.IsFavoritableID(id) {
			return nil
		}
	}
	if !m.toggleFavorite(m.provider, id) {
		return nil
	}

	m.refreshProviderListsAfterMutation()
	return nil
}

// toggleFavorite shares persistence feedback without decorating item metadata.
func (m *Model) toggleFavorite(prov playlist.Provider, id string) bool {
	ft, ok := prov.(provider.FavoriteToggler)
	if !ok || id == "" {
		return false
	}
	added, name, err := ft.ToggleFavorite(id)
	if err != nil {
		m.status.Errorf(statusTTLDefault, "Favorite save failed: %s", err)
		return false
	}
	if added {
		m.status.Showf(statusTTLMedium, "Favorited: %s", name)
	} else {
		m.status.Showf(statusTTLMedium, "Removed: %s", name)
	}
	return true
}

// playlistStarAction keeps the key handler and its help scoped to the same
// selection. Saved local playlists retain their per-playlist bookmark meaning.
type playlistStarAction uint8

const (
	starUnavailable playlistStarAction = iota
	starBookmark
	starRadioFavorite
)

func (m Model) selectedPlaylistStarAction() playlistStarAction {
	if m.playlist == nil || m.focus != focusPlaylist || m.plCursor < 0 || m.plCursor >= m.playlist.Len() {
		return starUnavailable
	}
	if m.loadedPlaylist != "" {
		if _, ok := m.localProvider.(provider.BookmarkSetter); ok {
			return starBookmark
		}
		return starUnavailable
	}
	if m.radioFavorites != nil {
		track, _ := m.playlist.Track(m.plCursor)
		if _, ok := radio.StationFromTrack(track); ok {
			return starRadioFavorite
		}
	}
	return starUnavailable
}

func (m *Model) togglePlaylistStar() tea.Cmd {
	action := m.selectedPlaylistStarAction()
	if action == starUnavailable {
		return nil
	}
	track, _ := m.playlist.Track(m.plCursor)
	switch action {
	case starBookmark:
		bs := m.localProvider.(provider.BookmarkSetter)
		if err := bs.SetBookmarkByPath(m.loadedPlaylist, track.Path); err != nil {
			m.status.Errorf(statusTTLDefault, "Save failed: %s", err)
			return nil
		}
		m.playlist.ToggleBookmark(m.plCursor)
		track, _ = m.playlist.Track(m.plCursor)
		if track.Bookmark {
			m.status.Showf(statusTTLDefault, "★ %s", track.DisplayName())
		} else {
			m.status.Showf(statusTTLDefault, "☆ %s", track.DisplayName())
		}
	case starRadioFavorite:
		station, _ := radio.StationFromTrack(track)
		added, err := m.radioFavorites.Toggle(station)
		if err != nil {
			m.status.Errorf(statusTTLDefault, "Favorite save failed: %s", err)
			return nil
		}
		if added {
			m.status.Showf(statusTTLMedium, "Favorited station: %s", station.Name)
		} else {
			m.status.Showf(statusTTLMedium, "Removed station: %s", station.Name)
		}
		// Only the displayed Radio pane needs refreshing. Switching back from
		// another provider fetches its list normally; never replace that provider's
		// list with Radio's results.
		if m.isActiveProvider("Radio") {
			m.refreshProviderListsAfterMutation()
		}
	}
	return nil
}

// playlistTrackStarred does not reuse Track.Bookmark for radio favorites.
func (m Model) playlistTrackStarred(track playlist.Track) bool {
	if m.loadedPlaylist == "" && m.radioFavorites != nil {
		if station, ok := radio.StationFromTrack(track); ok {
			return m.radioFavorites.Contains(station.URL)
		}
	}
	return track.Bookmark
}
