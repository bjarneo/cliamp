package ui

import (
	"fmt"

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
	// Don't load more while search results are shown.
	if rp.IsSearching() {
		return nil
	}
	// Load next page when within 10 items of the end.
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
		m.status.text = fmt.Sprintf("Favorited: %s", name)
	} else {
		m.status.text = fmt.Sprintf("Removed: %s", name)
	}
	m.status.ttl = statusTTLMedium

	// Refresh the provider list and try to keep the cursor on the same station.
	prevID := id
	if lists, err := rp.Playlists(); err == nil {
		m.providerLists = lists
		// Find the entry with the same ID or clamp.
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
