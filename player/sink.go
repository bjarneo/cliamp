package player

import (
	"errors"
	"fmt"
	"time"

	"github.com/gopxl/beep/v2"
	"github.com/gopxl/beep/v2/speaker"
)

// errFixedRate is returned by a sink that cannot follow the source sample rate.
var errFixedRate = errors.New("output device runs at a fixed sample rate")

// sink is the audio output backend the player writes to.
//
// Two implementations exist. beepSink drives gopxl/beep's package-level
// speaker: it works on every platform, but the process is locked to a single
// sample rate and every sample is reduced to 16 bits before it reaches the
// driver. alsaSink (Linux) owns an ALSA device directly, so it can reopen the
// device at each track's native rate and pass samples through untouched — the
// prerequisite for bit-perfect playback.
//
// Lock and Unlock guard the streamer chain: the backend never pulls samples
// while the lock is held, so callers can swap streamers without racing the
// audio thread. They carry the same semantics as speaker.Lock/Unlock.
type sink interface {
	Lock()
	Unlock()

	// Play installs the root streamer. Called once per player.
	Play(s beep.Streamer)

	Suspend() error
	Resume() error
	Close()

	// SampleRate is the rate the device is currently running at, in Hz.
	SampleRate() int
	// SetSampleRate reopens the device at rate and reports the rate actually
	// in use. A non-nil error means the request was not honoured; the returned
	// rate is then still valid and playback continues at it.
	SetSampleRate(rate int) (int, error)

	// Encoding is the PCM format samples are converted to for the device.
	Encoding() pcmEncoding
	// RateExact reports whether the device runs at exactly the requested rate
	// with no driver-side rate conversion in the path.
	RateExact() bool
}

// beepSink plays through gopxl/beep's package-level speaker. beep's speaker can
// only be initialized once per process and always converts to 16-bit signed
// PCM, so this sink is neither rate-switchable nor bit-transparent.
type beepSink struct {
	rate int
}

func newBeepSink(rate, bufferMs int) (*beepSink, error) {
	sr := beep.SampleRate(rate)
	if err := speaker.Init(sr, sr.N(time.Duration(bufferMs)*time.Millisecond)); err != nil {
		return nil, fmt.Errorf("speaker init: %w", err)
	}
	return &beepSink{rate: rate}, nil
}

func (b *beepSink) Lock()                 { speaker.Lock() }
func (b *beepSink) Unlock()               { speaker.Unlock() }
func (b *beepSink) Play(s beep.Streamer)  { speaker.Play(s) }
func (b *beepSink) Suspend() error        { return speaker.Suspend() }
func (b *beepSink) Resume() error         { return speaker.Resume() }
func (b *beepSink) Close()                { speaker.Clear() }
func (b *beepSink) SampleRate() int       { return b.rate }
func (b *beepSink) Encoding() pcmEncoding { return pcmS16LE }
func (b *beepSink) RateExact() bool       { return false }

func (b *beepSink) SetSampleRate(rate int) (int, error) {
	if rate == b.rate {
		return b.rate, nil
	}
	return b.rate, errFixedRate
}
