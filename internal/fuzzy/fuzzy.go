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
// Chinese, Japanese and Korean titles have no spaces between words, so each
// Han, Hiragana, Katakana and Hangul rune starts a word of its own: "天使テーゼ"
// finds "残酷な天使のテーゼ".
//
// A query that starts with ^ or ' is one literal phrase instead, spaces
// included and case ignored: "^queens of the stone age" finds the names that
// start with it, "'reload" the names that contain it.
package fuzzy

import (
	"slices"
	"strings"
	"sync"
	"sync/atomic"
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

// startsWord reports whether t[i] starts a word: it is the first rune, it
// follows a separator, or it is a CJK rune, which is a word by itself.
func startsWord(t []rune, i int) bool {
	return i == 0 || isSeparator(t[i-1]) || isCJK(t[i])
}

// isCJK reports whether r is a Han, Hiragana, Katakana or Hangul rune.
func isCJK(r rune) bool {
	return r >= 0x1100 && unicode.In(r, unicode.Han, unicode.Hiragana, unicode.Katakana, unicode.Hangul)
}

// compiledQuery stores pre-lowered query words so repeated Match calls with
// the same query skip re-splitting and re-lowering.
type compiledQuery struct {
	raw    string
	empty  bool
	phrase []rune
	prefix bool
	words  [][]rune
}

var lastCompiled atomic.Pointer[compiledQuery]

func compileQuery(query string) *compiledQuery {
	cq := &compiledQuery{raw: query}
	if strings.TrimSpace(query) == "" {
		cq.empty = true
		return cq
	}
	switch trimmed := strings.TrimLeft(query, " "); trimmed[0] {
	case '^':
		cq.phrase = appendLower(nil, trimmed[1:])
		cq.prefix = true
		if len(cq.phrase) == 0 {
			cq.empty = true
		}
		return cq
	case '\'':
		cq.phrase = appendLower(nil, trimmed[1:])
		if len(cq.phrase) == 0 {
			cq.empty = true
		}
		return cq
	}

	type span struct{ start, end int }
	flat := make([]rune, 0, len(query))
	var spans []span
	for w := range strings.FieldsSeq(query) {
		start := len(flat)
		flat = appendLower(flat, w)
		spans = append(spans, span{start: start, end: len(flat)})
	}
	words := make([][]rune, len(spans))
	for i, sp := range spans {
		words[i] = flat[sp.start:sp.end:sp.end]
	}
	cq.words = words
	return cq
}

// Match reports whether every word of query matches target and, if so, a
// relevance score where higher is a better match. An empty query matches
// every target with score 0.
func Match(query, target string) (score int, ok bool) {
	cq := lastCompiled.Load()
	if cq == nil || cq.raw != query {
		cq = compileQuery(query)
		lastCompiled.Store(cq)
	}
	if cq.empty {
		return 0, true
	}
	if cq.phrase != nil {
		return matchCompiledPhrase(cq.phrase, target, cq.prefix)
	}
	// Most targets miss a word altogether. Reject those before the scoring.
	for _, w := range cq.words {
		if !isLowerSubsequence(w, target) {
			return 0, false
		}
	}
	sc := scratchPool.Get().(*scratch)
	defer func() {
		sc.q = nil
		scratchPool.Put(sc)
	}()
	sc.t = appendLower(sc.t[:0], target)
	sc.starts = sc.starts[:0]
	for i := range sc.t {
		sc.starts = append(sc.starts, startsWord(sc.t, i))
	}
	for _, w := range cq.words {
		sc.q = w
		s, ok := sc.matchWord()
		if !ok {
			return 0, false
		}
		score += s
	}
	return score, true
}

// matchCompiledPhrase reports whether target starts with phrase, or with prefix
// false contains it, ignoring case. A match at the start ranks highest, then
// one at a word start.
func matchCompiledPhrase(q []rune, target string, prefix bool) (int, bool) {
	if len(q) == 0 {
		return 0, true
	}
	sc := scratchPool.Get().(*scratch)
	defer scratchPool.Put(sc)
	sc.t = appendLower(sc.t[:0], target)
	t := sc.t
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
		case startsWord(t, i):
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

// isLowerSubsequence reports whether the pre-lowered runes of qr appear in
// order in target, ignoring case.
func isLowerSubsequence(qr []rune, target string) bool {
	i := 0
	for _, r := range target {
		if i == len(qr) {
			break
		}
		if unicode.ToLower(r) == qr[i] {
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
