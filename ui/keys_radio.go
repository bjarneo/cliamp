package ui

import (
	tea "github.com/charmbracelet/bubbletea"

	"cliamp/external/radio"
)

// maybeLoadRadioBatch triggers a catalog batch fetch when the cursor is near the
// bottom of the provider list and more stations are available.
func (m *Model) maybeLoadRadioBatch() tea.Cmd {
	rp, ok := m.provider.(*radio.Provider)
	if !ok {
		return nil
	}
	if m.radioBatch.loading || m.radioBatch.done {
		return nil
	}
	if rp.IsSearching() {
		return nil
	}
	if m.provCursor >= len(m.providerLists)-10 {
		m.radioBatch.loading = true
		return fetchRadioBatchCmd(m.radioBatch.offset, radioBatchSize)
	}
	return nil
}

// toggleProviderFavorite toggles favorite status for the current entry in the
// provider list (only works for catalog, search, and favorite entries).
func (m *Model) toggleProviderFavorite() tea.Cmd {
	rp, ok := m.provider.(*radio.Provider)
	if !ok || len(m.providerLists) == 0 {
		return nil
	}
	id := m.providerLists[m.provCursor].ID
	if !radio.IsCatalogOrFavID(id) {
		return nil
	}
	added, name, err := rp.ToggleFavorite(id)
	if err != nil {
		return nil
	}
	if added {
		m.status.Showf(statusTTLMedium, "Favorited: %s", name)
	} else {
		m.status.Showf(statusTTLMedium, "Removed: %s", name)
	}

	prevID := id
	if lists, err := rp.Playlists(); err == nil {
		m.providerLists = lists
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
