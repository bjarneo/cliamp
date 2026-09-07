//go:build !linux

package player

// audioOutputHint returns no extra advice on platforms where a failed device
// open has no single common remedy.
func audioOutputHint() string {
	return ""
}
