package kittyimg

import (
	"os"
	"strings"
)

// SupportsGraphics reports whether the current terminal advertises support
// for the Kitty graphics protocol with Unicode placeholders, which Show
// draws with. It is deliberately conservative: WezTerm speaks the protocol
// but draws no placeholders, and callers can still offer Blocks as an
// explicit fallback.
func SupportsGraphics() bool {
	term := strings.ToLower(os.Getenv("TERM"))
	program := strings.ToLower(os.Getenv("TERM_PROGRAM"))
	return strings.Contains(term, "kitty") || strings.Contains(term, "ghostty") ||
		strings.Contains(program, "kitty") || strings.Contains(program, "ghostty")
}
