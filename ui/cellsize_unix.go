//go:build !windows

package ui

import (
	"os"

	"golang.org/x/sys/unix"
)

// terminalCellAspect returns the terminal's cell height divided by its width,
// measured rather than assumed: the window size ioctl reports the window in
// both cells and pixels, and dividing one by the other gives the cell box.
//
// Returns 0 when the terminal does not report pixel dimensions, which is
// common over plain ssh and on older emulators; the caller then falls back to
// a sensible constant.
// terminalCellPixels returns the size of one cell in pixels, or ok=false when
// the terminal does not report its window in pixels.
func terminalCellPixels() (width, height float64, ok bool) {
	for _, f := range []*os.File{os.Stdout, os.Stderr, os.Stdin} {
		ws, err := unix.IoctlGetWinsize(int(f.Fd()), unix.TIOCGWINSZ)
		if err != nil || ws == nil {
			continue
		}
		if ws.Col == 0 || ws.Row == 0 || ws.Xpixel == 0 || ws.Ypixel == 0 {
			continue
		}
		width = float64(ws.Xpixel) / float64(ws.Col)
		height = float64(ws.Ypixel) / float64(ws.Row)
		if width <= 0 || height <= 0 {
			continue
		}
		return width, height, true
	}
	return 0, 0, false
}

func terminalCellAspect() float64 {
	for _, f := range []*os.File{os.Stdout, os.Stderr, os.Stdin} {
		ws, err := unix.IoctlGetWinsize(int(f.Fd()), unix.TIOCGWINSZ)
		if err != nil || ws == nil {
			continue
		}
		if ws.Col == 0 || ws.Row == 0 || ws.Xpixel == 0 || ws.Ypixel == 0 {
			continue
		}
		cellW := float64(ws.Xpixel) / float64(ws.Col)
		cellH := float64(ws.Ypixel) / float64(ws.Row)
		if cellW <= 0 || cellH <= 0 {
			continue
		}
		aspect := cellH / cellW
		// A plausible cell is between square-ish and very tall; anything else
		// means the numbers are not what we think they are.
		if aspect < 1 || aspect > 5 {
			continue
		}
		return aspect
	}
	return 0
}
