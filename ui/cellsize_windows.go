//go:build windows

package ui

// terminalCellAspect has no window-size ioctl to consult on Windows, so the
// caller keeps its default.
func terminalCellAspect() float64 { return 0 }

// terminalCellPixels has no window-size ioctl to consult on Windows.
func terminalCellPixels() (width, height float64, ok bool) { return 0, 0, false }
