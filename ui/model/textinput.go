package model

import "unicode/utf8"

// removeLastRune trims the final UTF-8 rune from s, used by all text input handlers.
func removeLastRune(s string) string {
	if len(s) > 0 {
		_, size := utf8.DecodeLastRuneInString(s)
		return s[:len(s)-size]
	}
	return s
}
