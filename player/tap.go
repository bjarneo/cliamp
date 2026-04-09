// Package player provides the audio engine for MP3 playback with
// a 10-band parametric EQ, volume control, and sample capture for visualization.
package player

import (
	"sync/atomic"

	"github.com/gopxl/beep/v2"
)

// tap is a streamer wrapper that copies samples into a ring buffer
// for real-time FFT visualization. It sits in the audio pipeline
// between the volume control and the speaker controller.
//
// The write position is updated atomically, allowing the audio thread
// (sole writer) and the UI thread (infrequent reader at 50ms intervals)
// to operate without mutex contention. Minor sample tearing at the
// read boundary is invisible in FFT-based spectrum visualization.
type tap struct {
	s    beep.Streamer
	buf  []float64
	pos  atomic.Int64
	size int
}

// newTap wraps a streamer with a ring buffer of the given size.
func newTap(s beep.Streamer, bufSize int) *tap {
	return &tap{
		s:    s,
		buf:  make([]float64, bufSize),
		size: bufSize,
	}
}

// Stream passes audio through while capturing a mono mix into the ring buffer.
func (t *tap) Stream(samples [][2]float64) (int, bool) {
	n, ok := t.s.Stream(samples)
	p := int(t.pos.Load())
	for i := range n {
		t.buf[p] = (samples[i][0] + samples[i][1]) / 2
		p = (p + 1) % t.size
	}
	t.pos.Store(int64(p))
	return n, ok
}

// Err returns the underlying streamer's error.
func (t *tap) Err() error {
	return t.s.Err()
}

// SamplesInto copies the last len(dst) samples into dst, avoiding allocation.
// Uses two copy() calls for the ring buffer wraparound instead of per-element
// modulo, which is significantly faster for large buffers (e.g. FFT size 2048).
func (t *tap) SamplesInto(dst []float64) int {
	n := min(len(dst), t.size)
	p := int(t.pos.Load())
	start := (p - n + t.size) % t.size
	if start+n <= t.size {
		copy(dst, t.buf[start:start+n])
	} else {
		first := t.size - start
		copy(dst[:first], t.buf[start:])
		copy(dst[first:], t.buf[:n-first])
	}
	return n
}
