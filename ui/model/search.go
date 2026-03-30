package model

import (
	"strings"
)

// updateSearch filters the playlist by the current search query.
func (m *Model) updateSearch() {
	m.search.results = nil
	m.search.cursor = 0
	if m.search.query == "" {
		return
	}
	query := strings.ToLower(m.search.query)
	for i, t := range m.playlist.Tracks() {
		if strings.Contains(strings.ToLower(t.DisplayName()), query) {
			m.search.results = append(m.search.results, i)
		}
	}
}
