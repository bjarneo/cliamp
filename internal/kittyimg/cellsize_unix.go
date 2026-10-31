//go:build unix

package kittyimg

import (
	"os"

	"golang.org/x/sys/unix"
)

// CellSize returns the terminal cell size in pixels when the tty reports it.
func CellSize() (width, height float64, ok bool) {
	for _, file := range []*os.File{os.Stdout, os.Stdin} {
		ws, err := unix.IoctlGetWinsize(int(file.Fd()), unix.TIOCGWINSZ)
		if err != nil || ws.Col == 0 || ws.Row == 0 || ws.Xpixel == 0 || ws.Ypixel == 0 {
			continue
		}
		return float64(ws.Xpixel) / float64(ws.Col), float64(ws.Ypixel) / float64(ws.Row), true
	}
	return 0, 0, false
}
