// player/sink_alsa_other.go — stub for platforms without a bit-perfect backend.

//go:build !linux || !cgo

package player

import "errors"

// newBitPerfectSink reports that bit-perfect output is not implemented here.
// The player falls back to the beep speaker and reports bit-perfect as
// unavailable rather than failing to start.
func newBitPerfectSink(device string, rate, bufferMs int, channels channelLayout) (sink, error) {
	return nil, errors.New("bit-perfect output is only implemented for ALSA on Linux")
}
