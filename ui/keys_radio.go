package ui

import (
	tea "github.com/charmbracelet/bubbletea"

	"cliamp/provider"
)

// maybeLoadCatalogBatch triggers a catalog batch fetch when the cursor is near the
// bottom of the provider list and more entries are available.
func (m *Model) maybeLoadCatalogBatch() tea.Cmd {
	loader, ok := m.provider.(provider.CatalogLoader)
	if !ok {
		return nil
	}
	if m.catalogBatch.loading || m.catalogBatch.done {
		return nil
	}
	if cs, ok := m.provider.(provider.CatalogSearcher); ok && cs.IsSearching() {
		return nil
	}
	if m.provCursor >= len(m.providerLists)-10 {
		m.catalogBatch.loading = true
		return fetchCatalogBatchCmd(loader, m.catalogBatch.offset, catalogBatchSize)
	}
	return nil
}

// toggleProviderFavorite toggles favorite status for the current entry in the
// provider list (only works for providers implementing FavoriteToggler + SectionedList).
func (m *Model) toggleProviderFavorite() tea.Cmd {
	ft, ok := m.provider.(provider.FavoriteToggler)
	if !ok || len(m.providerLists) == 0 {
		return nil
	}
	id := m.providerLists[m.provCursor].ID
	if sl, ok := m.provider.(provider.SectionedList); ok {
		if !sl.IsFavoritableID(id) {
			return nil
		}
	}
	added, name, err := ft.ToggleFavorite(id)
	if err != nil {
		return nil
	}
	if added {
		m.status.Showf(statusTTLMedium, "Favorited: %s", name)
	} else {
		m.status.Showf(statusTTLMedium, "Removed: %s", name)
	}

	prevID := id
	if lists, err := m.provider.Playlists(); err == nil {
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
