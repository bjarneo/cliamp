//go:build darwin || dragonfly || freebsd || linux || netbsd || openbsd

package session

import (
	"fmt"

	"golang.org/x/sys/unix"
)

// keepNewlineTranslation re-enables output post-processing on a terminal that
// was just put into raw mode.
//
// Bubble Tea maps newlines to CRLF when the program's input is not a terminal,
// which is exactly what a session host gives it: the renderer then writes a
// bare newline between rows and counts on the terminal to return to column
// zero. Full raw mode turns that translation off, so every row after the first
// would start where the previous one ended -- a screen that drifts right and
// wraps into itself. Raw input is what the player needs; cooked newlines are
// what its renderer assumes, and a terminal can do both.
func keepNewlineTranslation(fd uintptr) error {
	termios, err := unix.IoctlGetTermios(int(fd), getTermios)
	if err != nil {
		return fmt.Errorf("read terminal modes: %w", err)
	}
	termios.Oflag |= unix.OPOST | unix.ONLCR
	if err := unix.IoctlSetTermios(int(fd), setTermios, termios); err != nil {
		return fmt.Errorf("set terminal modes: %w", err)
	}
	return nil
}
