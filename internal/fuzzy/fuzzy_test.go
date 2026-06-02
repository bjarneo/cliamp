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
