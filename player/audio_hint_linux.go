//go:build linux

package player

// audioOutputHint returns a parenthetical suffix appended to a failed audio
// device open. On Linux cliamp outputs through ALSA, so a machine running
// PipeWire or PulseAudio without the ALSA bridge plugin has no usable
// "default" PCM. That is the most common cause of the failure, and it is
// what WSL2 hits out of the box.
func audioOutputHint() string {
	return " (cliamp outputs through ALSA; on a PipeWire or PulseAudio system install the" +
		" ALSA bridge package: pipewire-alsa, pulseaudio-alsa, or libasound2-plugins on" +
		" Debian/Ubuntu and WSL2. See docs/configuration.md)"
}
