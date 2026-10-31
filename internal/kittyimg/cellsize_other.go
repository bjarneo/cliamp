//go:build !unix

package kittyimg

// CellSize reports no pixel geometry on platforms without a tty winsize
// ioctl. Callers use the conventional 1:2 cell aspect as a fallback.
func CellSize() (width, height float64, ok bool) {
	return 0, 0, false
}
