// Package cellwidth measures the terminal width of styled text. For the text
// a frame is mostly made of it counts runes, which is exact there and much
// cheaper than ansi.StringWidth's grapheme segmentation.
package cellwidth

import (
	"unicode/utf8"

	"github.com/charmbracelet/x/ansi"
)

// String is the width of s in cells, as ansi.StringWidth gives it.
func String(s string) int {
	if w, ok := fast(s); ok {
		return w
	}
	return ansi.StringWidth(s)
}

// fast counts the cells of s if every rune is one cell and every escape
// sequence is CSI.
func fast(s string) (int, bool) {
	w := 0
	for i := 0; i < len(s); {
		c := s[i]
		switch {
		case c >= 0x20 && c < 0x7f:
			w++
			i++
		case c == 0x1b:
			if i+1 >= len(s) || s[i+1] != '[' {
				return 0, false
			}
			j := i + 2
			for j < len(s) && s[j] >= 0x20 && s[j] <= 0x3f {
				j++
			}
			if j >= len(s) || s[j] < 0x40 || s[j] > 0x7e {
				return 0, false
			}
			i = j + 1
		case c < 0x80:
			return 0, false
		default:
			r, n := utf8.DecodeRuneInString(s[i:])
			if !narrow(r) {
				return 0, false
			}
			w++
			i += n
		}
	}
	return w, true
}

// narrow reports whether r is one cell and never joins the rune before it:
// Latin letters, punctuation, box drawing, blocks, shapes and braille.
func narrow(r rune) bool {
	switch {
	case r >= 0xa0 && r <= 0x24f:
		return r != 0xad // soft hyphen
	case r >= 0x2010 && r <= 0x2027:
		return true
	case r >= 0x2500 && r <= 0x25ff:
		return r != 0x25fd && r != 0x25fe // wide emoji squares
	case r >= 0x2800 && r <= 0x28ff:
		return true
	}
	return false
}
