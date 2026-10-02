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
	if m.provPane.cursor >= len(m.provPane.lists)-10 {
		m.catalogBatch.loading = true
		return m.fetchCatalogBatch(loader)
	}
	return nil
}

// answerLocationPrompt records the listener's answer to the location question
// and refreshes the pane, where the offer row is replaced by their country.
func (m *Model) answerLocationPrompt(allowed bool) tea.Cmd {
	m.provPane.askLoc = false
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
	m.provPane.loading = true
	return m.fetchProviderPlaylists()
}

// toggleProviderFavorite toggles favorite status for the current entry in the
// provider list when the provider supports it.
func (m *Model) toggleProviderFavorite() tea.Cmd {
	if m.provPane.loading || m.provPane.cursor < 0 || m.provPane.cursor >= len(m.provPane.lists) || m.selectedProviderListIsBrowseEntry() {
		return nil
	}
	id := m.provPane.lists[m.provPane.cursor].ID
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
// selection. A directory radio station outside saved playlists keeps its
// station favorite. Every other track toggles the ♥ favorite.
type playlistStarAction uint8

const (
	starUnavailable playlistStarAction = iota
	starFavorite
	starRadioFavorite
)

func (m Model) selectedPlaylistStarAction() playlistStarAction {
	if m.playlist == nil || m.focus != focusPlaylist || m.plCursor < 0 || m.plCursor >= m.playlist.Len() {
		return starUnavailable
	}
	track, _ := m.playlist.Track(m.plCursor)
	if _, ok := m.favoriteStation(track, m.loadedPlaylist != ""); ok {
		return starRadioFavorite
	}
	if m.favStore != nil {
		return starFavorite
	}
	return starUnavailable
}

// favoriteStation returns the station whose favorite the ♥ of track shows.
// savedPlaylist is true for a row of a loaded saved playlist. ok is false when
// the track uses the favorites store. It is the rule of trackFavorited.
func (m Model) favoriteStation(track playlist.Track, savedPlaylist bool) (radio.CatalogStation, bool) {
	if savedPlaylist || m.radioFavorites == nil {
		return radio.CatalogStation{}, false
	}
	return radio.StationFromTrack(track)
}

// savedPlaylistRow reports whether track is a row of a loaded saved playlist.
// It matches rows by path. IPC clients send a track that can come from the
// queue or from a provider list, and only a queue row uses the saved rule.
func (m Model) savedPlaylistRow(track playlist.Track) bool {
	if m.loadedPlaylist == "" || m.playlist == nil {
		return false
	}
	for _, row := range m.playlist.Tracks() {
		if row.Path == track.Path {
			return true
		}
	}
	return false
}

func (m *Model) togglePlaylistStar() tea.Cmd {
	if m.selectedPlaylistStarAction() == starUnavailable {
		return nil
	}
	track, _ := m.playlist.Track(m.plCursor)
	cmd, err := m.togglePlaylistTrackFavorite(track, m.loadedPlaylist != "")
	if err != nil {
		m.status.Errorf(statusTTLDefault, "Favorite failed: %s", err)
		return nil
	}
	return cmd
}

// togglePlaylistTrackFavorite toggles the ♥ of track in the store that the ♥
// marker reads. savedPlaylist is true for a row of a loaded saved playlist. A
// directory radio station outside a saved playlist toggles its station
// favorite. Every other track toggles the favorites store. The f key and IPC
// playlist.bookmark use it.
func (m *Model) togglePlaylistTrackFavorite(track playlist.Track, savedPlaylist bool) (tea.Cmd, error) {
	station, ok := m.favoriteStation(track, savedPlaylist)
	if !ok {
		return m.toggleTrackFavorite(track)
	}
	added, err := m.radioFavorites.Toggle(station)
	if err != nil {
		return nil, err
	}
	if added {
		m.status.Showf(statusTTLMedium, "Favorited station: %s", station.Name)
	} else {
		m.status.Showf(statusTTLMedium, "Removed station: %s", station.Name)
	}
	// Only the displayed Radio pane needs refreshing. Switching back from
	// another provider fetches its list normally; never replace that provider's
	// list with Radio's results.
	if m.activeProviderKey() == providerKeyRadio {
		m.refreshProviderListsAfterMutation()
	}
	return nil, nil
}

// playlistTrackFavorited reports whether a playback row shows the ♥ marker.
// It uses the same rule as selectedPlaylistStarAction, so the marker always
// shows the state that f toggles. The render path calls it, so it must not do
// I/O.
func (m Model) playlistTrackFavorited(track playlist.Track) bool {
	return trackFavorited(track, m.favSet, m.radioFavorites, m.loadedPlaylist != "")
}

// trackFavoriteLookup returns the ♥ rule as a function that is safe to call
// from a tea.Cmd. It captures the current favSet, which refreshFavSet
// replaces and never changes in place. Set playback for rows of the playback
// playlist, so a saved playlist uses track favorites as its rows do.
func (m Model) trackFavoriteLookup(playback bool) func(playlist.Track) bool {
	favSet, stations := m.favSet, m.radioFavorites
	saved := playback && m.loadedPlaylist != ""
	return func(track playlist.Track) bool {
		return trackFavorited(track, favSet, stations, saved)
	}
}

// trackFavorited reports the ♥ state of track. A directory radio station
// outside a saved playlist uses its station favorite. Every other track uses
// the favorites store.
func trackFavorited(track playlist.Track, favSet map[string]struct{}, stations *radio.Favorites, savedPlaylist bool) bool {
	if !savedPlaylist && stations != nil {
		if station, ok := radio.StationFromTrack(track); ok {
			return stations.Contains(station.URL)
		}
	}
	_, ok := favSet[track.Path]
	return ok
}
