// Package fuzzy provides lightweight case-insensitive fuzzy matching with
// relevance scoring. It ranks in-memory search results (the playlist search,
// the provider browser and the file browser filters) without an external
// dependency, and stays small and allocation-light on the hot path of every
// keystroke.
//
// A query is split into words at whitespace, and every word must match, in
// any order. A query word matches when its characters appear in order and the
// match enters each target word at its first character: "mtlc rld" finds
// "Metallica - Reload" and "lst" finds "Love Story", but "opeth" does not find
// "Polyphia - Deny the Flesh". A query word that is a literal substring of the
// target also matches, below those.
//
// A query that starts with ^ or ' is one literal phrase instead, spaces
// included and case ignored: "^queens of the stone age" finds the names that
// start with it, "'reload" the names that contain it.
package fuzzy

import (
	"slices"
	"strings"
	"sync"
	"unicode"
)

// Scoring weights. In order of preference, a candidate ranks higher when the
// match starts at the very beginning of the string, follows a word boundary,
// or forms a run of consecutive matched characters.
const (
	scoreMatch       = 1 // base score for each matched rune
	bonusFirstChar   = 6 // matched rune is the first rune of the target
	bonusBoundary    = 4 // matched rune follows a word separator
	bonusConsecutive = 4 // matched rune immediately follows the previous match
)

// noMatch marks an impossible state in the scoring table.
const noMatch = -1

// isSeparator reports whether r delimits a word for boundary scoring.
func isSeparator(r rune) bool {
	switch r {
	case ' ', '-', '_', '/', '\\', '.', ':', '(', ')', '[', ']':
		return true
	}
	return false
}

// Match reports whether every word of query matches target and, if so, a
// relevance score where higher is a better match. An empty query matches
// every target with score 0.
func Match(query, target string) (score int, ok bool) {
	if strings.TrimSpace(query) == "" {
		return 0, true
	}
	switch trimmed := strings.TrimLeft(query, " "); trimmed[0] {
	case '^':
		return matchPhrase(trimmed[1:], target, true)
	case '\'':
		return matchPhrase(trimmed[1:], target, false)
	}
	// Most targets miss a word altogether. Reject those before the scoring.
	for w := range strings.FieldsSeq(query) {
		if !isSubsequence(w, target) {
			return 0, false
		}
	}
	sc := scratchPool.Get().(*scratch)
	defer scratchPool.Put(sc)
	sc.t = appendLower(sc.t[:0], target)
	sc.starts = sc.starts[:0]
	for i := range sc.t {
		sc.starts = append(sc.starts, i == 0 || isSeparator(sc.t[i-1]))
	}
	for w := range strings.FieldsSeq(query) {
		sc.q = appendLower(sc.q[:0], w)
		s, ok := sc.matchWord()
		if !ok {
			return 0, false
		}
		score += s
	}
	return score, true
}

// matchPhrase reports whether target starts with phrase, or with prefix
// false contains it, ignoring case. A match at the start ranks highest, then
// one at a word start.
func matchPhrase(phrase, target string, prefix bool) (int, bool) {
	sc := scratchPool.Get().(*scratch)
	defer scratchPool.Put(sc)
	sc.q = appendLower(sc.q[:0], phrase)
	sc.t = appendLower(sc.t[:0], target)
	q, t := sc.q, sc.t
	if len(q) == 0 {
		return 0, true
	}
	for i := 0; i+len(q) <= len(t); i++ {
		if !slices.Equal(t[i:i+len(q)], q) {
			if prefix {
				break
			}
			continue
		}
		score := len(q) * scoreMatch
		switch {
		case i == 0:
			score += bonusFirstChar
		case isSeparator(t[i-1]):
			score += bonusBoundary
		}
		return score, true
	}
	return 0, false
}

// appendLower appends the lowered runes of s to buf.
func appendLower(buf []rune, s string) []rune {
	for _, r := range s {
		buf = append(buf, unicode.ToLower(r))
	}
	return buf
}

// scratch holds the buffers of one Match call, reused across calls so a
// search over a long playlist does not allocate per track.
type scratch struct {
	q, t      []rune
	starts    []bool
	prev, cur []int
}

var scratchPool = sync.Pool{New: func() any { return new(scratch) }}

// isSubsequence reports whether the runes of q appear in order in target,
// ignoring case. Every match of q needs this, so it is a cheap first filter.
func isSubsequence(q, target string) bool {
	qr := []rune(q)
	i := 0
	for _, r := range target {
		if i == len(qr) {
			break
		}
		if unicode.ToLower(r) == unicode.ToLower(qr[i]) {
			i++
		}
	}
	return i == len(qr)
}

// matchWord scores the best match of the query word q in the lowered target
// t. A query rune may match a target rune that starts a word, or one in the
// same word as the rune before it. Of all such matches it returns the best
// score.
func (sc *scratch) matchWord() (int, bool) {
	q, t, starts := sc.q, sc.t, sc.starts
	if len(q) > len(t) {
		return 0, false
	}
	// prev[j] is the best score with the previous query rune at target j.
	prev := resize(sc.prev, len(t))
	cur := resize(sc.cur, len(t))
	defer func() { sc.prev, sc.cur = prev, cur }()
	for i, qr := range q {
		bestBefore := noMatch // best prev[k] for any k < j
		bestInWord := noMatch // best prev[k] for k < j in the word of j
		for j, tr := range t {
			if starts[j] {
				bestInWord = noMatch
			}
			cur[j] = noMatch
			if tr == qr {
				bonus := 0
				switch {
				case j == 0:
					bonus = bonusFirstChar
				case starts[j]:
					bonus = bonusBoundary
				}
				if i == 0 {
					if starts[j] {
						cur[j] = scoreMatch + bonus
					}
				} else {
					from := bestInWord
					if starts[j] {
						from = bestBefore
					}
					if j > 0 && prev[j-1] != noMatch && prev[j-1]+bonusConsecutive > from {
						from = prev[j-1] + bonusConsecutive
					}
					if from != noMatch {
						cur[j] = from + scoreMatch + bonus
					}
				}
			}
			if i > 0 && prev[j] > bestBefore {
				bestBefore = prev[j]
			}
			if i > 0 && prev[j] > bestInWord {
				bestInWord = prev[j]
			}
		}
		prev, cur = cur, prev
	}
	best := noMatch
	for _, s := range prev {
		best = max(best, s)
	}
	if best != noMatch {
		return best, true
	}
	// A literal substring that starts inside a word still matches, below the
	// matches that follow word starts.
	if containsRunes(t, q) {
		return len(q)*scoreMatch + (len(q)-1)*bonusConsecutive/2, true
	}
	return 0, false
}

// resize returns buf with length n, reusing its storage when it is large
// enough.
func resize(buf []int, n int) []int {
	if cap(buf) < n {
		return make([]int, n)
	}
	return buf[:n]
}

// containsRunes reports whether q occurs in t as a contiguous run.
func containsRunes(t, q []rune) bool {
	for i := 0; i+len(q) <= len(t); i++ {
		if slices.Equal(t[i:i+len(q)], q) {
			return true
		}
	}
	return false
}
