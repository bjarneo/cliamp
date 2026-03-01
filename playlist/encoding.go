package playlist

import (
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/encoding"
	"golang.org/x/text/encoding/charmap"
)

// legacyEncodings lists codepages commonly used by music taggers that wrote
// non-Latin text but marked the encoding as Latin-1.
var legacyEncodings = []encoding.Encoding{
	charmap.Windows1255, // Hebrew
	charmap.Windows1256, // Arabic
	charmap.Windows1251, // Cyrillic
	charmap.Windows1253, // Greek
	charmap.Windows874,  // Thai
}

// sanitizeTag detects mojibake from legacy codepages and re-decodes to UTF-8.
//
// Many old ID3 taggers write non-Latin text using a Windows codepage but mark
// the encoding as Latin-1. The tag library faithfully decodes those bytes as
// Latin-1, producing garbled text. We detect this by checking for a high
// density of Latin-1 supplement characters (U+0080–U+00FF), reverse the
// Latin-1 decode to recover the original bytes, then try common codepages
// and pick the one that produces the most non-Latin script characters.
// ASCII bytes are identical across all codepages, so mixed-script text is
// handled correctly.
func sanitizeTag(s string) string {
	if s == "" {
		return s
	}

	// Count runes in the Latin-1 supplement range (U+0080–U+00FF).
	// Real Latin text (French, German) rarely exceeds ~10% accented chars.
	// Mojibake from non-Latin scripts is almost entirely in this range.
	var total, highCount int
	for _, r := range s {
		total++
		if r >= 0x80 && r <= 0xFF {
			highCount++
		}
	}
	if total == 0 || highCount*3 < total {
		return s
	}

	// Reverse the Latin-1 decode to recover the original tag bytes.
	raw := make([]byte, 0, total)
	for _, r := range s {
		if r > 0xFF {
			return s // Contains runes outside Latin-1 — not simple mojibake
		}
		raw = append(raw, byte(r))
	}

	// The original bytes might be valid UTF-8 that was double-decoded.
	if utf8.Valid(raw) {
		return string(raw)
	}

	// Try legacy codepages; pick the one that produces the most
	// non-Latin letters (Hebrew, Cyrillic, Arabic, Greek, Thai, etc.).
	var bestText string
	var bestScore int
	for _, enc := range legacyEncodings {
		decoded, err := enc.NewDecoder().Bytes(raw)
		if err != nil {
			continue
		}
		text := string(decoded)
		if !utf8.ValidString(text) {
			continue
		}
		score := 0
		for _, r := range text {
			if unicode.IsLetter(r) && r > 0x024F {
				score++
			}
		}
		if score > bestScore {
			bestScore = score
			bestText = text
		}
	}

	if bestText != "" {
		return bestText
	}
	return s
}
