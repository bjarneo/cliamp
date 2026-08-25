// Package player provides the audio engine for MP3 playback with
// a 10-band parametric EQ, volume control, and sample capture for visualization.
package player

import (
	"sync/atomic"
	"time"

	"github.com/gopxl/beep/v2"
)

// tap is a streamer wrapper that copies samples into a ring buffer
// for real-time FFT visualization. It sits in the audio pipeline
// before the volume control so the visualizer sees pre-volume amplitude.
//
// The write position is updated atomically, allowing the audio thread
// (sole writer) and the visualizer thread to operate without mutex contention.
// Minor sample tearing at the read boundary is invisible in visualization.
type tap struct {
	s           beep.Streamer
	buf         [][2]float64
	pos         atomic.Int64
	written     atomic.Int64 // total frames written; used by the waveform sample clock
	writeAt     atomic.Int64 // Unix nanoseconds of the latest write
	writeFrames atomic.Int64 // frame count of the latest write
	size        int
	sampleRate  int
	now         func() time.Time
}

// newTap wraps a streamer with a ring buffer of the given size.
func newTap(s beep.Streamer, bufSize, sampleRate int) *tap {
	return &tap{
		s:          s,
		buf:        make([][2]float64, bufSize),
		size:       bufSize,
		sampleRate: sampleRate,
		now:        time.Now,
	}
}

// Stream passes audio through while capturing stereo frames in the ring buffer.
func (t *tap) Stream(samples [][2]float64) (int, bool) {
	n, ok := t.s.Stream(samples)
	p := int(t.pos.Load())
	for i := range n {
		t.buf[p] = samples[i]
		p = (p + 1) % t.size
	}
	t.writeFrames.Store(int64(n))
	t.writeAt.Store(t.now().UnixNano())
	t.pos.Store(int64(p))
	t.written.Add(int64(n))
	return n, ok
}

// Err returns the underlying streamer's error.
func (t *tap) Err() error {
	return t.s.Err()
}

// SamplesInto copies a mono mix of the last len(dst) frames into dst without
// allocating or using per-sample modulo in the ring-buffer read loop.
func (t *tap) SamplesInto(dst []float64) int {
	return t.samplesIntoAt(dst, t.written.Load())
}

// WaveformSamplesInto copies samples from the most recent output buffer at its
// real-time playback position. This keeps raw visualizers moving between the
// audio backend's larger buffer refills.
func (t *tap) WaveformSamplesInto(dst []float64) int {
	frames := t.writeFrames.Load()
	if frames <= 0 || t.sampleRate <= 0 {
		return 0
	}
	elapsed := int64(t.now().Sub(time.Unix(0, t.writeAt.Load())) * time.Duration(t.sampleRate) / time.Second)
	elapsed = max(0, min(elapsed, frames))
	end := t.written.Load() - frames + elapsed
	return t.samplesIntoAt(dst, end)
}

func (t *tap) samplesIntoAt(dst []float64, end int64) int {
	n := min(len(dst), t.size)
	if n == 0 || end <= 0 {
		return 0
	}
	n = min(n, int(end))
	start := int((end - int64(n)) % int64(t.size))
	if start < 0 {
		start += t.size
	}
	first := min(n, t.size-start)
	for i, sample := range t.buf[start : start+first] {
		dst[i] = (sample[0] + sample[1]) / 2
	}
	for i, sample := range t.buf[:n-first] {
		dst[first+i] = (sample[0] + sample[1]) / 2
	}
	return n
}

// StereoSamplesInto copies the last len(dst) stereo frames into dst.
func (t *tap) StereoSamplesInto(dst [][2]float64) int {
	n := min(len(dst), t.size)
	p := int(t.pos.Load())
	start := (p - n + t.size) % t.size
	first := min(n, t.size-start)
	copy(dst[:first], t.buf[start:start+first])
	copy(dst[first:n], t.buf[:n-first])
	return n
}
