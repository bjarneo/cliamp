package fuzzy

import "testing"

func TestMatch(t *testing.T) {
	tests := []struct {
		name   string
		query  string
		target string
		want   bool
	}{
		{"empty query matches", "", "anything", true},
		{"exact substring", "love", "Love Story", true},
		{"case insensitive", "LOVE", "love story", true},
		{"non-contiguous subsequence", "lst", "Love Story", true},
		{"missing char", "lovex", "Love Story", false},
		{"out of order", "ba", "ab", false},
		{"empty target non-empty query", "x", "", false},
		{"unicode subsequence", "café", "Le Café", true},
		{"words in any order", "reload metallica", "Metallica — Reload (1997)", true},
		{"abbreviated words", "mtlc rld", "Metallica — Reload (1997)", true},
		{"every word must match", "metallica load x", "Metallica — Reload (1997)", false},
		{"scattered across words", "opeth", "Polyphia - DENY THE FLESH", false},
		{"word must start at a word start", "tlc", "Metallica", false},
		{"literal substring inside a word", "allica", "Metallica", true},
		{"separator inside a query word", "ac/dc", "AC/DC - Thunderstruck", true},
		{"whitespace-only query matches", "  ", "anything", true},
		{"prefix phrase", "^queens of the stone age", "Queens of the Stone Age — Villains (2017)", true},
		{"prefix phrase is not fuzzy", "^opeth", "Eyes Wide Open — Through Life and Death (2021)", false},
		{"prefix phrase must start the name", "^stone age", "Queens of the Stone Age — R (2000)", false},
		{"prefix phrase keeps its separator", "^Opeth — ", "Opethian — Demo (2020)", false},
		{"contains phrase", "'stone age", "Queens of the Stone Age — R (2000)", true},
		{"contains phrase is not fuzzy", "'mtl", "Metallica — Reload (1997)", false},
		{"lone marker matches", "^", "anything", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, ok := Match(tt.query, tt.target); ok != tt.want {
				t.Fatalf("Match(%q, %q) ok = %v, want %v", tt.query, tt.target, ok, tt.want)
			}
		})
	}
}

func TestMatchRanking(t *testing.T) {
	// Each case asserts that `query` scores strictly higher against `high`
	// than against `low` (both must match).
	tests := []struct {
		name      string
		query     string
		high, low string
	}{
		{"prefix beats interior", "lov", "Love", "Beloved"},
		{"word boundary beats mid-word", "st", "Love Story", "august"},
		{"consecutive beats scattered", "abc", "abcdef", "axbxcx"},
		{"start beats later", "fo", "Foo Bar", "Bar Foo"},
		{"word start beats a later letter", "load", "Metallica — Load (1996)", "Metallica — Reload (1997)"},
		{"word start beats substring", "lica", "Lica Lu", "Metallica"},
		{"contains phrase at a word start beats mid-word", "'load", "Metallica — Load (1996)", "Metallica — Reload (1997)"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			hi, okHi := Match(tt.query, tt.high)
			lo, okLo := Match(tt.query, tt.low)
			if !okHi || !okLo {
				t.Fatalf("both should match: high ok=%v, low ok=%v", okHi, okLo)
			}
			if hi <= lo {
				t.Fatalf("Match(%q,%q)=%d should beat Match(%q,%q)=%d",
					tt.query, tt.high, hi, tt.query, tt.low, lo)
			}
		})
	}
}
