package model

import "cliamp/provider"

// navSortLabel resolves a sort-type ID to its human label for the active
// provider's album browser, falling back to the raw ID.
func (m Model) navSortLabel(sortID string) string {
	if ab, ok := m.navBrowser.prov.(provider.AlbumBrowser); ok {
		for _, st := range ab.AlbumSortTypes() {
			if st.ID == sortID {
				return st.Label
			}
		}
	}
	return sortID
}
