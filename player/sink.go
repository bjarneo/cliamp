package player

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/gopxl/beep/v2"
	"github.com/gopxl/beep/v2/speaker"
)

// errFixedRate is returned by a sink that cannot follow the source sample rate.
var errFixedRate = errors.New("output device runs at a fixed sample rate")

// channelLayout selects which physical output channels carry cliamp's
// stereo signal. Most interfaces are natively 2-channel and never need this,
// but some multichannel audio interfaces have no native stereo mode at all —
// their one PCM substream only accepts a fixed, larger channel count — so
// there is no way to reach them with plain 2-channel negotiation. The zero
// value (Configured false) is that plain 2-channel case, unchanged from
// before this type existed: a hard request for exactly 2 channels, which
// either matches the device or fails outright. Configured true instead
// widens the request to whatever the device needs to fit Left and Right
// (see channelLayout.total and configureALSA in sink_alsa_linux.go), and
// every other channel in each frame carries silence. Only alsaSink (Linux)
// implements this; beepSink ignores it entirely — see bitperfect_channels
// in the config.
type channelLayout struct {
	Configured  bool
	Left, Right int // 0-based channel indices within the negotiated frame
}

// total is the minimum channel count a device must offer to fit both Left
// and Right.
func (c channelLayout) total() int {
	return max(c.Left, c.Right) + 1
}

// parseChannelLayout parses a "left,right" pair of 0-based channel indices
// (e.g. "0,1" for the first channel pair, "2,3" for the next) — the format
// bitperfect_channels/--bitperfect-channels take. An empty string is the
// zero value (Configured false, plain 2-channel negotiation), not an error.
func parseChannelLayout(s string) (channelLayout, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return channelLayout{}, nil
	}
	l, r, ok := strings.Cut(s, ",")
	if !ok {
		return channelLayout{}, fmt.Errorf("bitperfect_channels %q: want \"left,right\", e.g. \"0,1\"", s)
	}
	left, err := strconv.Atoi(strings.TrimSpace(l))
	if err != nil || left < 0 {
		return channelLayout{}, fmt.Errorf("bitperfect_channels %q: left channel must be a non-negative integer", s)
	}
	right, err := strconv.Atoi(strings.TrimSpace(r))
	if err != nil || right < 0 {
		return channelLayout{}, fmt.Errorf("bitperfect_channels %q: right channel must be a non-negative integer", s)
	}
	if left == right {
		return channelLayout{}, fmt.Errorf("bitperfect_channels %q: left and right must be different channels", s)
	}
	return channelLayout{Configured: true, Left: left, Right: right}, nil
}

// ValidateChannels reports whether s is a valid Quality.Channels value,
// without needing a Player — for validating --bitperfect-channels/
// bitperfect_channels early, at CLI-parse time, the same way --log-level is.
func ValidateChannels(s string) error {
	_, err := parseChannelLayout(s)
	return err
}

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
	// RealRate returns an independently confirmed hardware rate when one is
	// available (see alsaDevice.verifiedRate), or 0 when it isn't — in which
	// case callers should fall back to SampleRate(). Reporting-only.
	RealRate() int
	// Err returns the reason the configured output device stopped working, or
	// nil when it hasn't. A non-nil Err does not necessarily mean playback has
	// stopped — a backend may recover onto a fallback device on its own (see
	// alsaSink.handleFatal) — only that the originally configured device is no
	// longer the one in use. Reporting-only, like RealRate.
	Err() error
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
func (b *beepSink) RealRate() int         { return 0 }
func (b *beepSink) Err() error            { return nil }

func (b *beepSink) SetSampleRate(rate int) (int, error) {
	if rate == b.rate {
		return b.rate, nil
	}
	return b.rate, errFixedRate
}
