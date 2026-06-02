// Package fuzzy provides lightweight case-insensitive fuzzy subsequence
// matching with relevance scoring. It ranks in-memory search results (the
// playlist search and the file browser filter) without an external dependency:
// a greedy subsequence scan with a few positional bonuses is enough to rank
// track and file names, and stays small and allocation-light on the hot path
// of every keystroke.
package fuzzy

import "unicode"

// Scoring weights. In order of preference, a candidate ranks higher when the
// match starts at the very beginning of the string, follows a word boundary,
// or forms a run of consecutive matched characters.
const (
	scoreMatch       = 1 // base score for each matched rune
	bonusFirstChar   = 6 // matched rune is the first rune of the target
	bonusBoundary    = 4 // matched rune follows a word separator
	bonusConsecutive = 4 // matched rune immediately follows the previous match
)

// isSeparator reports whether r delimits a word for boundary scoring.
func isSeparator(r rune) bool {
	switch r {
	case ' ', '-', '_', '/', '\\', '.', ':', '(', ')', '[', ']':
		return true
	}
	return false
}

// Match reports whether query is a case-insensitive subsequence of target and,
// if so, a relevance score where higher is a better match. An empty query
// matches every target with score 0. ok is false when query is not a
// subsequence of target.
func Match(query, target string) (score int, ok bool) {
	if query == "" {
		return 0, true
	}
	q := []rune(query)
	qi := 0
	qlow := unicode.ToLower(q[0]) // current query rune, folded once per advance
	ti := 0                       // rune index within target
	lastMatch := -2               // rune index of the previous matched rune
	var prev rune                 // previous target rune, for boundary detection
	for _, r := range target {
		if unicode.ToLower(r) == qlow {
			score += scoreMatch
			switch {
			case ti == 0:
				score += bonusFirstChar
			case isSeparator(prev):
				score += bonusBoundary
			}
			if ti == lastMatch+1 {
				score += bonusConsecutive
			}
			lastMatch = ti
			qi++
			if qi == len(q) {
				return score, true
			}
			qlow = unicode.ToLower(q[qi])
		}
		prev = r
		ti++
	}
	return 0, false
}
