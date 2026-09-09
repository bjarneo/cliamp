package model

import (
	tea "charm.land/bubbletea/v2"

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

	prevID := id
	if lists, err := m.provider.Playlists(); err == nil {
		m.providerLists = providerListsWithBrowse(m.provider, lists)
		for i, p := range m.providerLists {
			if p.ID == prevID {
				m.provCursor = i
				return nil
			}
		}
		if m.provCursor >= len(m.providerLists) {
			m.provCursor = max(0, len(m.providerLists)-1)
		}
	}
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
