package model

import (
	"sort"

	"github.com/bjarneo/cliamp/internal/fuzzy"
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
	tracks := m.playlist.Tracks()
	m.search.results = fuzzyRank(m.search.query, len(tracks), func(i int) string {
		return tracks[i].DisplayName()
	})
}

// fuzzyRank returns the indices of the n items whose text fuzzy-matches
// query, best match first; equal scores keep their list order.
func fuzzyRank(query string, n int, text func(int) string) []int {
	type match struct{ idx, score int }
	matches := make([]match, 0, n)
	for i := range n {
		if score, ok := fuzzy.Match(query, text(i)); ok {
			matches = append(matches, match{i, score})
		}
	}
	sort.SliceStable(matches, func(a, b int) bool {
		return matches[a].score > matches[b].score
	})
	var results []int
	for _, mt := range matches {
		results = append(results, mt.idx)
	}
	return results
}
