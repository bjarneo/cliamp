// player/sink_alsa_linux.go — bit-perfect ALSA output for Linux.
//
// The default output path (beep's speaker over oto) is locked to one sample
// rate for the whole process, converts every sample to 16-bit, and leaves
// ALSA's rate converter enabled. This sink talks to ALSA directly instead so
// cliamp can hand the device the decoder's samples untouched: the device is
// (re)opened at the track's native rate, and samples are written in the
// widest format the device accepts.
//
// Whether rate conversion is disabled during negotiation depends on the
// device. For real hardware ("hw:", "plughw:") the negotiation asks for the
// exact rate with resampling disabled first — that's the thing that
// guarantees no ALSA-level conversion happens. For a sound-server-backed
// device ("default", "pipewire", "pulse", ...) that order is reversed: with
// resampling disabled, PipeWire's ALSA compatibility layer has been observed
// to silently hand back whatever rate its shared graph is already running at
// instead of retuning to match — no ALSA error, so the fallback that would
// otherwise fix it never triggers. Asking with resampling allowed first lets
// PipeWire's own rate-matching retune the graph to the exact rate when it's
// configured to allow it (see docs/audio-quality.md), which is both the only
// way to get it to retune at all and still bit-perfect once it does — the
// negotiated rate is verified against what was asked for either way.
// PipeWire/PulseAudio can still resample under the hood in ways this can't
// detect; point bitperfect_device at a hardware device for a hard guarantee.

//go:build linux && cgo

package player

/*
#cgo pkg-config: alsa
#include <alsa/asoundlib.h>
#include <stdlib.h>
*/
import "C"

import (
	"fmt"
	"strings"
	"sync"
	"unsafe"

	"github.com/gopxl/beep/v2"

	"github.com/bjarneo/cliamp/applog"
)

// defaultALSADevice is used when no device is configured. It routes through
// whatever sound server owns the card, which is what most desktops need.
const defaultALSADevice = "default"

// alsaPeriodsPerBuffer splits the requested buffer into this many periods. Two
// is ALSA's classic minimum; four keeps the write loop responsive enough that a
// device reopen (on a sample-rate change) is not audible as a long stall.
const alsaPeriodsPerBuffer = 4

// alsaEncodings lists the sample formats to try, widest first. S32_LE and
// FLOAT_LE both carry 16- and 24-bit sources losslessly; S16_LE is the last
// resort and only lossless for 16-bit material.
var alsaEncodings = []pcmEncoding{pcmS32LE, pcmFloat32LE, pcmS16LE}

// alsaSink writes PCM straight to an ALSA device and can reopen that device at
// a different sample rate while the player's streamer chain stays alive.
type alsaSink struct {
	device   string
	bufferMs int

	// mu guards the streamer chain. It is held only while pulling samples,
	// never while blocking on the device, and is what Lock/Unlock expose.
	mu  sync.Mutex
	src beep.Streamer

	// devMu serializes device lifecycle changes (open, reopen, close).
	devMu sync.Mutex
	dev   *alsaDevice
	shut  bool

	// stateCond parks the writer goroutine while suspended so an idle player
	// costs no CPU, mirroring speaker.Suspend.
	stateMu   sync.Mutex
	stateCond *sync.Cond
	suspended bool

	// reservation holds a temporary D-Bus device reservation acquired for a
	// real hardware device (see reserve_linux.go), so a sound server yields
	// it for the sink's lifetime and reclaims it automatically on Close. Nil
	// when the device is virtual or the reservation attempt failed — either
	// way playback proceeds via the ordinary snd_pcm_open attempt.
	reservation *deviceReservation
}

// alsaDevice is one open incarnation of the device. Reopening at a new rate
// retires the current one: its stop channel is closed, its writer goroutine
// signals done, and only then is the handle closed — writing to a closed ALSA
// handle would crash the process.
type alsaDevice struct {
	handle *C.snd_pcm_t
	rate   int // the app-facing negotiated rate; what the PCM handle itself expects, and what pipeline/reopen decisions must use
	enc    pcmEncoding
	period int  // frames per write
	exact  bool // device accepted the requested rate with conversion disabled
	// verifiedRate is the kernel-confirmed rate of the underlying hardware
	// substream (see hwparams_linux.go), which can differ from rate when a
	// conversion layer (plughw:, a sound server) silently resampled. Zero
	// when it couldn't be independently confirmed. Only ever used for
	// reporting (BitPerfectStatus) — never for pipeline/reopen decisions,
	// which must keep targeting what the PCM handle itself expects.
	verifiedRate int
	stop         chan struct{}
	done         chan struct{}
}

// newBitPerfectSink opens a bit-perfect capable sink at the given rate.
// It starts suspended, matching the player's idle-until-first-Play behaviour.
func newBitPerfectSink(device string, rate, bufferMs int) (sink, error) {
	if device == "" {
		device = defaultALSADevice
	}
	s := &alsaSink{device: device, bufferMs: bufferMs, suspended: true}
	s.stateCond = sync.NewCond(&s.stateMu)

	var reserveErr error
	if !isVirtualALSADevice(device) {
		if idx, ok := alsaCardIndex(device); ok {
			if res, err := acquireDeviceReservation(idx); err == nil {
				s.reservation = res
				applog.Debug("reserve: acquired %s for %s", reserveBusName(idx), device)
			} else {
				reserveErr = err
				applog.Debug("reserve: %v", err)
			}
		}
	}

	s.devMu.Lock()
	defer s.devMu.Unlock()
	if err := s.openLocked(rate); err != nil {
		s.reservation.release()
		s.reservation = nil
		if reserveErr != nil {
			return nil, fmt.Errorf("%w (device reservation also unavailable: %v)", err, reserveErr)
		}
		return nil, err
	}
	return s, nil
}

func (s *alsaSink) Lock()   { s.mu.Lock() }
func (s *alsaSink) Unlock() { s.mu.Unlock() }

func (s *alsaSink) Play(src beep.Streamer) {
	s.mu.Lock()
	s.src = src
	s.mu.Unlock()
}

func (s *alsaSink) Clear() {
	s.mu.Lock()
	s.src = nil
	s.mu.Unlock()
}

func (s *alsaSink) Suspend() error {
	s.setSuspended(true)
	return nil
}

func (s *alsaSink) Resume() error {
	s.setSuspended(false)
	return nil
}

func (s *alsaSink) setSuspended(v bool) {
	s.stateMu.Lock()
	s.suspended = v
	s.stateMu.Unlock()
	s.stateCond.Broadcast()
}

func (s *alsaSink) Close() {
	s.Clear()
	s.devMu.Lock()
	defer s.devMu.Unlock()
	s.shut = true
	s.closeLocked()
	s.reservation.release()
	s.reservation = nil
}

func (s *alsaSink) SampleRate() int {
	s.devMu.Lock()
	defer s.devMu.Unlock()
	if s.dev == nil {
		return 0
	}
	return s.dev.rate
}

func (s *alsaSink) Encoding() pcmEncoding {
	s.devMu.Lock()
	defer s.devMu.Unlock()
	if s.dev == nil {
		return pcmS16LE
	}
	return s.dev.enc
}

func (s *alsaSink) RateExact() bool {
	s.devMu.Lock()
	defer s.devMu.Unlock()
	return s.dev != nil && s.dev.exact
}

// RealRate returns the kernel-confirmed rate of the underlying hardware
// substream, or 0 when it couldn't be independently confirmed (see
// alsaDevice.verifiedRate). Reporting-only — see that field's doc comment.
func (s *alsaSink) RealRate() int {
	s.devMu.Lock()
	defer s.devMu.Unlock()
	if s.dev == nil {
		return 0
	}
	return s.dev.verifiedRate
}

// SetSampleRate reopens the device at rate. When the device rejects it, the
// previous rate is restored so playback continues (resampled by the caller) and
// the reason is returned.
func (s *alsaSink) SetSampleRate(rate int) (int, error) {
	s.devMu.Lock()
	defer s.devMu.Unlock()

	if s.shut {
		return 0, fmt.Errorf("alsa sink is closed")
	}
	if s.dev != nil && s.dev.rate == rate {
		return rate, nil
	}

	prev := 0
	if s.dev != nil {
		prev = s.dev.rate
	}
	s.closeLocked()

	rateErr := s.openLocked(rate)
	if rateErr == nil {
		return s.dev.rate, nil
	}
	if prev == 0 {
		return 0, rateErr
	}
	if err := s.openLocked(prev); err != nil {
		return 0, fmt.Errorf("reopen at %d Hz failed (%v) and fallback to %d Hz failed: %w", rate, rateErr, prev, err)
	}
	return prev, rateErr
}

// openLocked opens the device at rate and starts its writer goroutine.
// devMu must be held.
func (s *alsaSink) openLocked(rate int) error {
	dev, err := openALSADevice(s.device, rate, s.bufferMs)
	if err != nil {
		return err
	}
	s.dev = dev
	go s.writeLoop(dev)
	return nil
}

// closeLocked retires the current device, waiting for its writer goroutine to
// leave snd_pcm_writei before closing the handle. devMu must be held.
func (s *alsaSink) closeLocked() {
	dev := s.dev
	if dev == nil {
		return
	}
	s.dev = nil
	close(dev.stop)
	// Wake the writer if it is parked on the suspend condition.
	s.stateCond.Broadcast()
	<-dev.done
	C.snd_pcm_close(dev.handle)
}

// writeLoop pulls samples from the streamer chain and writes them to the
// device until the device is retired or hits an unrecoverable error.
func (s *alsaSink) writeLoop(dev *alsaDevice) {
	defer close(dev.done)

	frames := make([][2]float64, dev.period)
	buf := make([]byte, dev.period*dev.enc.bytesPerFrame())

	for s.waitRunning(dev) {
		s.mu.Lock()
		src := s.src
		n := 0
		if src != nil {
			n, _ = src.Stream(frames)
		}
		s.mu.Unlock()
		// The player's root streamer always fills the buffer, but a detached or
		// exhausted source must still produce a full period of silence or the
		// device underruns.
		clear(frames[n:])

		if !s.writeFrames(dev, buf[:encodePCM(frames, buf, dev.enc)]) {
			return
		}
	}
}

// waitRunning blocks while the sink is suspended and reports whether the
// device is still current.
func (s *alsaSink) waitRunning(dev *alsaDevice) bool {
	s.stateMu.Lock()
	defer s.stateMu.Unlock()
	for {
		select {
		case <-dev.stop:
			return false
		default:
		}
		if !s.suspended {
			return true
		}
		s.stateCond.Wait()
	}
}

// writeFrames writes one encoded period, recovering from underruns. It returns
// false when the device was retired or the error is unrecoverable.
func (s *alsaSink) writeFrames(dev *alsaDevice, buf []byte) bool {
	frameSize := dev.enc.bytesPerFrame()
	for len(buf) > 0 {
		select {
		case <-dev.stop:
			return false
		default:
		}
		n := C.snd_pcm_writei(dev.handle, unsafe.Pointer(&buf[0]), C.snd_pcm_uframes_t(len(buf)/frameSize))
		if n < 0 {
			// Underruns are expected after a suspend/resume cycle: recover
			// (which re-prepares the stream) and retry the same period.
			if r := C.snd_pcm_recover(dev.handle, C.int(n), 1); r < 0 {
				return false
			}
			continue
		}
		buf = buf[int(n)*frameSize:]
	}
	return true
}

// isVirtualALSADevice reports whether device is likely backed by a sound
// server's ALSA compatibility layer rather than talking to hardware
// directly. Direct hardware devices are named "hw:..." or "plughw:...";
// everything else ("default", "pipewire", "pulse", a named PCM from
// ~/.asoundrc, ...) is treated as possibly virtual. See the file-level
// comment for why this changes negotiation order.
func isVirtualALSADevice(device string) bool {
	return !strings.HasPrefix(device, "hw:") && !strings.HasPrefix(device, "plughw:")
}

// isRawHardwareDevice reports whether device is ALSA's "hw:" plugin
// specifically — the one PCM type with no conversion capability at all: a
// request either matches the hardware exactly or the open fails outright.
// "plughw:" shares the "hw:" prefix check above (it does talk to real
// hardware) but wraps it in alsa-lib's automatic format/channel/rate
// conversion layer, entirely in userspace — a request can succeed there via
// silent conversion the same way a sound server's virtual device can, so it
// must not be trusted for rate-exactness any more than one is.
func isRawHardwareDevice(device string) bool {
	return strings.HasPrefix(device, "hw:")
}

// openALSADevice opens device and negotiates the best configuration for rate.
// Exact-rate configurations are preferred over wider sample formats: a rate
// mismatch means the driver resamples, which bit-perfect mode exists to avoid.
// The order resampling is tried in depends on the device type — see the
// file-level comment.
func openALSADevice(device string, rate, bufferMs int) (*alsaDevice, error) {
	resampleOrder := [2]bool{false, true}
	if isVirtualALSADevice(device) {
		resampleOrder = [2]bool{true, false}
	}

	var firstErr error
	for _, allowResample := range resampleOrder {
		for _, enc := range alsaEncodings {
			dev, err := tryOpenALSA(device, rate, bufferMs, enc, allowResample)
			if err == nil {
				return dev, nil
			}
			if firstErr == nil {
				firstErr = err
			}
		}
	}
	return nil, fmt.Errorf("alsa %q at %d Hz: %w", device, rate, firstErr)
}

// tryOpenALSA opens and fully configures the device for one candidate
// format/resampling combination. A fresh handle is used per attempt because a
// rejected snd_pcm_hw_params leaves the previous one in an unusable state.
func tryOpenALSA(device string, rate, bufferMs int, enc pcmEncoding, allowResample bool) (*alsaDevice, error) {
	cname := C.CString(device)
	defer C.free(unsafe.Pointer(cname))

	var handle *C.snd_pcm_t
	if err := C.snd_pcm_open(&handle, cname, C.SND_PCM_STREAM_PLAYBACK, 0); err < 0 {
		return nil, fmt.Errorf("open: %s", alsaStrError(err))
	}

	cardIdx, hasCard := 0, false
	if !isVirtualALSADevice(device) {
		cardIdx, hasCard = alsaCardIndex(device)
	}
	devIdx := alsaDeviceIndex(device)

	dev, err := configureALSA(handle, rate, bufferMs, enc, allowResample, isRawHardwareDevice(device), cardIdx, devIdx, hasCard)
	if err != nil {
		C.snd_pcm_close(handle)
		return nil, err
	}
	return dev, nil
}

// configureALSA applies the hardware parameters and reports the resulting
// device state. It never resamples unless allowResample is set. rawHardware
// indicates the device is ALSA's non-converting "hw:" plugin; cardIdx/devIdx
// (valid only when hasCard) identify the underlying card for the
// /proc-based real-rate check — see the exact field below for why both
// matter.
func configureALSA(handle *C.snd_pcm_t, rate, bufferMs int, enc pcmEncoding, allowResample bool, rawHardware bool, cardIdx, devIdx int, hasCard bool) (*alsaDevice, error) {
	var params *C.snd_pcm_hw_params_t
	if err := C.snd_pcm_hw_params_malloc(&params); err < 0 {
		return nil, fmt.Errorf("hw_params_malloc: %s", alsaStrError(err))
	}
	defer C.snd_pcm_hw_params_free(params)

	if err := C.snd_pcm_hw_params_any(handle, params); err < 0 {
		return nil, fmt.Errorf("hw_params_any: %s", alsaStrError(err))
	}
	if err := C.snd_pcm_hw_params_set_access(handle, params, C.SND_PCM_ACCESS_RW_INTERLEAVED); err < 0 {
		return nil, fmt.Errorf("set_access: %s", alsaStrError(err))
	}
	if err := C.snd_pcm_hw_params_set_format(handle, params, alsaFormat(enc)); err < 0 {
		return nil, fmt.Errorf("set_format %s: %s", enc, alsaStrError(err))
	}
	if err := C.snd_pcm_hw_params_set_channels(handle, params, 2); err < 0 {
		return nil, fmt.Errorf("set_channels: %s", alsaStrError(err))
	}

	resample := C.uint(0)
	if allowResample {
		resample = 1
	}
	if err := C.snd_pcm_hw_params_set_rate_resample(handle, params, resample); err < 0 {
		return nil, fmt.Errorf("set_rate_resample: %s", alsaStrError(err))
	}

	// A resampling attempt may settle for a neighbouring rate (set_rate_near);
	// a non-resampling attempt must get the exact rate or fail (set_rate,
	// dir=0). Either way the actual rate is read back below via get_rate.
	if allowResample {
		actual := C.uint(rate)
		if err := C.snd_pcm_hw_params_set_rate_near(handle, params, &actual, nil); err < 0 {
			return nil, fmt.Errorf("set_rate_near %d: %s", rate, alsaStrError(err))
		}
	} else if err := C.snd_pcm_hw_params_set_rate(handle, params, C.uint(rate), 0); err < 0 {
		return nil, fmt.Errorf("set_rate %d: %s", rate, alsaStrError(err))
	}

	// Split the requested buffer into periods so each write covers a fraction
	// of it; ALSA rounds both to what the hardware supports.
	bufferFrames := C.snd_pcm_uframes_t(rate * bufferMs / 1000)
	if bufferFrames < 256 {
		bufferFrames = 256
	}
	periodFrames := bufferFrames / alsaPeriodsPerBuffer
	if err := C.snd_pcm_hw_params_set_buffer_size_near(handle, params, &bufferFrames); err < 0 {
		return nil, fmt.Errorf("set_buffer_size: %s", alsaStrError(err))
	}
	if err := C.snd_pcm_hw_params_set_period_size_near(handle, params, &periodFrames, nil); err < 0 {
		return nil, fmt.Errorf("set_period_size: %s", alsaStrError(err))
	}
	if err := C.snd_pcm_hw_params(handle, params); err < 0 {
		return nil, fmt.Errorf("hw_params: %s", alsaStrError(err))
	}

	// Read back what the device actually settled on — the rate may have been
	// rounded even when set_rate succeeded.
	var finalRate C.uint
	var dir C.int
	if err := C.snd_pcm_hw_params_get_rate(params, &finalRate, &dir); err < 0 {
		return nil, fmt.Errorf("get_rate: %s", alsaStrError(err))
	}
	if err := C.snd_pcm_hw_params_get_period_size(params, &periodFrames, nil); err < 0 {
		return nil, fmt.Errorf("get_period_size: %s", alsaStrError(err))
	}
	if periodFrames <= 0 {
		return nil, fmt.Errorf("device reported a zero period size")
	}

	// The rate the app-facing handle reports (finalRate) is only trustworthy
	// on a raw "hw:" device: anything else (plughw:, a sound server) can
	// silently convert a request it can't honor and still report success.
	// Prefer the kernel's own view of the underlying hardware substream —
	// which a conversion layer sits in front of, not inside — so a match is
	// verified against reality rather than assumed from the device string.
	var verifiedRate int
	exact := false
	if hasCard {
		if real, ok := realALSARateSettled(cardIdx, devIdx); ok {
			verifiedRate = real
			exact = real == rate
		}
	}
	if !exact && rawHardware {
		// /proc lookup unavailable (non-standard environment, multiple
		// subdevices, ...): fall back to the app-level report, safe only
		// because "hw:" has no conversion layer to hide a mismatch behind.
		exact = int(finalRate) == rate && dir == 0
	}

	return &alsaDevice{
		handle:       handle,
		rate:         int(finalRate),
		enc:          enc,
		period:       int(periodFrames),
		exact:        exact,
		verifiedRate: verifiedRate,
		stop:         make(chan struct{}),
		done:         make(chan struct{}),
	}, nil
}

// alsaFormat maps a pcmEncoding to its ALSA format constant.
func alsaFormat(enc pcmEncoding) C.snd_pcm_format_t {
	switch enc {
	case pcmS32LE:
		return C.SND_PCM_FORMAT_S32_LE
	case pcmFloat32LE:
		return C.SND_PCM_FORMAT_FLOAT_LE
	default:
		return C.SND_PCM_FORMAT_S16_LE
	}
}

func alsaStrError(err C.int) string {
	return C.GoString(C.snd_strerror(err))
}
