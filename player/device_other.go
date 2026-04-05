// player/device_other.go — stub for non-macOS platforms.

//go:build !darwin || ios || !cgo

package player

// DeviceSampleRate returns 0 on platforms where device detection is not
// implemented. Callers should fall back to a sensible default (e.g. 44100).
func DeviceSampleRate() int {
	return 0
}
