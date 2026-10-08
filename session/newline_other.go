//go:build !darwin && !dragonfly && !freebsd && !linux && !netbsd && !openbsd

package session

// keepNewlineTranslation is a no-op where Bubble Tea does not map newlines to
// CRLF (Windows) or where terminal modes are out of reach. See the Unix
// implementation for why the translation matters.
func keepNewlineTranslation(uintptr) error { return nil }
