package player

import "context"

// OutputSwitcher is an optional Engine capability for network outputs. Device
// identifiers are session-only; local audio_device configuration stays native.
type OutputSwitcher interface {
	OutputDevices(context.Context) ([]AudioDevice, error)
	SwitchOutput(context.Context, string, string) error
	RemoteOutput() bool
}
