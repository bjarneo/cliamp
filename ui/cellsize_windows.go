//go:build windows

package ui

// The Windows console does not report its window in pixels, so the cell
// aspect cannot be measured there and the caller falls back to a constant.
func terminalCellAspect() float64 { return 0 }
