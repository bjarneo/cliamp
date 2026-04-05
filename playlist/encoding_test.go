package playlist

import "testing"

func TestSanitizeTagCleanString(t *testing.T) {
	// Regular ASCII/Latin text should pass through unchanged
	got := sanitizeTag("Hello World")
	if got != "Hello World" {
		t.Errorf("sanitizeTag(%q) = %q, want unchanged", "Hello World", got)
	}
}

func TestSanitizeTagEmpty(t *testing.T) {
	if got := sanitizeTag(""); got != "" {
		t.Errorf("sanitizeTag empty = %q, want empty", got)
	}
}

func TestSanitizeTagLowDensityAccents(t *testing.T) {
	// French/German text with a few accented chars (< 1/3 high) should pass through
	input := "Beyonce feat. Jay-Z"
	got := sanitizeTag(input)
	if got != input {
		t.Errorf("sanitizeTag(%q) = %q, want unchanged", input, got)
	}
}

func TestSanitizeTagDoubleDecodedUTF8(t *testing.T) {
	// Simulate double-decoded UTF-8: the original UTF-8 bytes for "café"
	// (63 61 66 c3 a9) interpreted as Latin-1 produce: c a f Ã ©
	// The raw bytes c3 a9 are valid UTF-8 for 'é', so sanitizeTag should recover "café".
	input := "caf\u00c3\u00a9" // c a f U+00C3 U+00A9 (Latin-1 decode of UTF-8 "é")
	got := sanitizeTag(input)
	if got != "café" {
		t.Errorf("sanitizeTag(%q) = %q, want %q", input, got, "café")
	}
}

func TestSanitizeTagHighUnicode(t *testing.T) {
	// If the string contains runes > U+00FF, it can't be simple mojibake
	input := "日本語テスト"
	got := sanitizeTag(input)
	if got != input {
		t.Errorf("sanitizeTag(%q) = %q, want unchanged", input, got)
	}
}
