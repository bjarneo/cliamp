// player/audio_device_stub.go — fallback for platforms without device selection support.

//go:build !linux && (!darwin || ios || !cgo) && !windows

package player

import "fmt"

// ListAudioDevices is not available on this platform.
func ListAudioDevices() ([]AudioDevice, error) {
	return nil, fmt.Errorf("audio device selection is not available on this platform")
}

// PrepareAudioDevice is a no-op on unsupported platforms.
func PrepareAudioDevice(device string) func() { return func() {} }

// SwitchAudioDevice is not supported on this platform.
func SwitchAudioDevice(deviceName string) error {
	return fmt.Errorf("audio device switching is not available on this platform")
}
