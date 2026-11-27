// Package player provides the audio engine for MP3 playback with
// a 10-band parametric EQ, volume control, and sample capture for visualization.
package player

import (
	"math"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gopxl/beep/v2"
)

// framesToDuration converts sample frames at sr to a duration without int64 overflow.
func framesToDuration(frames int64, sr beep.SampleRate) time.Duration {
	if frames <= 0 || sr <= 0 {
		return 0
	}
	rate := int64(sr)
	secs := frames / rate
	rem := frames % rate
	if secs > math.MaxInt64/int64(time.Second) {
		return time.Duration(math.MaxInt64)
	}
	dur := secs * int64(time.Second)
	extra := (rem * int64(time.Second)) / rate
	if dur > math.MaxInt64-extra {
		return time.Duration(math.MaxInt64)
	}
	return time.Duration(dur + extra)
}

// durationToFrames converts d at sampleRate to sample frames without int64 overflow.
func durationToFrames(d time.Duration, sampleRate int) int64 {
	if d <= 0 || sampleRate <= 0 {
		return 0
	}
	rate := int64(sampleRate)
	secs := int64(d / time.Second)
	rem := int64(d % time.Second)
	if secs > math.MaxInt64/rate {
		return math.MaxInt64
	}
	frames := secs * rate
	extra := (rem * rate) / int64(time.Second)
	if frames > math.MaxInt64-extra {
		return math.MaxInt64
	}
	return frames + extra
}

// tap copies stereo frames into a ring buffer for real-time visualization.
// It sits before volume so visualizers read pre-volume levels.
// A short mutex guards the ring buffer and cursor state.
type tap struct {
	s beep.Streamer

	disabled atomic.Bool // false means capture is active

	mu      sync.Mutex
	buf     [][2]float64
	pos     int
	written int64     // total frames written for the waveform sample clock
	writeAt time.Time // timestamp of the latest write

	readMu  sync.Mutex
	readBuf [][2]float64

	size       int
	sampleRate int
	// speakerFrames is the audio backend buffer size in frames.
	// WaveformSamplesInto subtracts speakerFrames to read audible frames.
	speakerFrames int
	now           func() time.Time
}

// newTap wraps a streamer with a ring buffer of the given size. speakerFrames
// is the audio backend buffer size in frames.
func newTap(s beep.Streamer, bufSize, sampleRate, speakerFrames int) *tap {
	return &tap{
		s:             s,
		buf:           make([][2]float64, bufSize),
		size:          bufSize,
		sampleRate:    sampleRate,
		speakerFrames: max(0, speakerFrames),
		now:           time.Now,
	}
}

// SetEnabled controls whether Stream copies frames into the ring buffer.
func (t *tap) SetEnabled(enabled bool) {
	t.disabled.Store(!enabled)
}

// Stream passes audio through and captures stereo frames when enabled.
func (t *tap) Stream(samples [][2]float64) (int, bool) {
	n, ok := t.s.Stream(samples)
	if n == 0 || t.size <= 0 || t.disabled.Load() {
		return n, ok
	}
	now := t.now()
	t.mu.Lock()
	src := samples[:n]
	if len(src) >= t.size {
		src = src[len(src)-t.size:]
		p := (t.pos + n) % t.size
		first := copy(t.buf[p:], src)
		copy(t.buf[:], src[first:])
		t.pos = p
	} else {
		first := copy(t.buf[t.pos:], src)
		if first < len(src) {
			copy(t.buf[:], src[first:])
			t.pos = len(src) - first
		} else {
			t.pos += first
			if t.pos == t.size {
				t.pos = 0
			}
		}
	}
	t.written += int64(n)
	t.writeAt = now
	t.mu.Unlock()
	return n, ok
}

// Err returns the underlying streamer error.
func (t *tap) Err() error {
	return t.s.Err()
}

// SamplesInto copies a mono mix of the newest frames into dst.
// It copies raw stereo slices under t.mu and downmixes after unlock.
func (t *tap) SamplesInto(dst []float64) int {
	want := min(len(dst), t.size)
	if want <= 0 {
		return 0
	}
	t.readMu.Lock()
	defer t.readMu.Unlock()
	raw := t.ensureReadBufLocked(want)

	t.mu.Lock()
	n := t.copyRawAtLocked(raw, t.written)
	t.mu.Unlock()

	downmixMono(dst[:n], raw[:n])
	return n
}

// WaveformSamplesInto copies audible frames interpolated from the wall clock.
// It copies raw stereo slices under t.mu and downmixes after unlock.
func (t *tap) WaveformSamplesInto(dst []float64) int {
	if t.sampleRate <= 0 {
		return 0
	}
	want := min(len(dst), t.size)
	if want <= 0 {
		return 0
	}
	t.readMu.Lock()
	defer t.readMu.Unlock()
	raw := t.ensureReadBufLocked(want)

	now := t.now()
	t.mu.Lock()
	written := t.written
	var n int
	if written > 0 {
		end := written
		if t.speakerFrames > 0 {
			elapsed := durationToFrames(now.Sub(t.writeAt), t.sampleRate)
			if elapsed < int64(t.speakerFrames) {
				end = max(0, written-int64(t.speakerFrames)+elapsed)
			}
		}
		n = t.copyRawAtLocked(raw, end)
	}
	t.mu.Unlock()

	downmixMono(dst[:n], raw[:n])
	return n
}

func (t *tap) ensureReadBufLocked(n int) [][2]float64 {
	if cap(t.readBuf) < n {
		t.readBuf = make([][2]float64, n)
	} else {
		t.readBuf = t.readBuf[:n]
	}
	return t.readBuf
}

func (t *tap) copyRawAtLocked(dst [][2]float64, end int64) int {
	n := min(len(dst), t.size)
	if n == 0 || end <= 0 {
		return 0
	}
	if end < int64(n) {
		n = int(end)
	}
	start := int((end - int64(n)) % int64(t.size))
	if start < 0 {
		start += t.size
	}
	first := min(n, t.size-start)
	copy(dst[:first], t.buf[start:start+first])
	copy(dst[first:n], t.buf[:n-first])
	return n
}

func downmixMono(dst []float64, src [][2]float64) {
	for i, sample := range src {
		dst[i] = (sample[0] + sample[1]) * 0.5
	}
}

// StereoSamplesInto copies the last len(dst) stereo frames into dst.
func (t *tap) StereoSamplesInto(dst [][2]float64) int {
	t.mu.Lock()
	defer t.mu.Unlock()
	n := min(len(dst), t.size)
	if n == 0 {
		return 0
	}
	start := t.pos - n
	if start < 0 {
		start += t.size
	}
	first := min(n, t.size-start)
	copy(dst[:first], t.buf[start:start+first])
	copy(dst[first:n], t.buf[:n-first])
	return n
}
