package model

import (
	"sort"

	"cliamp/internal/fuzzy"
)

// updateSearch filters the playlist by the current search query, ranking
// matches by fuzzy relevance (best match first).
func (m *Model) updateSearch() {
	m.search.results = nil
	m.search.cursor = 0
	m.search.scroll = 0
	if m.search.query == "" {
		return
	}
	type match struct{ idx, score int }
	matches := make([]match, 0, m.playlist.Len())
	for i, t := range m.playlist.Tracks() {
		if score, ok := fuzzy.Match(m.search.query, t.DisplayName()); ok {
			matches = append(matches, match{i, score})
		}
	}
	sort.SliceStable(matches, func(a, b int) bool {
		return matches[a].score > matches[b].score
	})
	for _, mt := range matches {
		m.search.results = append(m.search.results, mt.idx)
	}
}
