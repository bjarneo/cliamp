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
	// speakerFrames is the size of the audio backend's own buffer, in frames.
	// Frames handed to Stream are not audible yet: they sit in that buffer for
	// up to speakerFrames/sampleRate seconds. The waveform read position has to
	// subtract it to follow what is actually being heard.
	speakerFrames int
	now           func() time.Time
}

// newTap wraps a streamer with a ring buffer of the given size. speakerFrames
// is the audio backend's buffer size in frames, used by WaveformSamplesInto to
// place the read position at what is currently audible.
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

// WaveformSamplesInto copies samples at the position that is currently
// audible, interpolated from the wall clock. Raw visualizers therefore advance
// on every frame they are rendered at, instead of once per backend refill.
//
// The backend does not ask for samples at a steady rate: it refills its buffer
// in bursts, then goes quiet for as long as that buffer takes to play. Anchoring
// the read position to the last Stream call meant the waveform advanced only
// through that call's frames and then froze until the next burst, so its
// effective refresh rate followed the buffer size rather than the tick rate
// (~7 FPS at the default 250 ms buffer, ~19 FPS at 50 ms).
//
// Anchoring it to the whole backend buffer instead makes the position advance
// continuously: at the moment of a write, what is audible is speakerFrames
// behind what has been written, and it moves forward with elapsed time.
func (t *tap) WaveformSamplesInto(dst []float64) int {
	written := t.written.Load()
	if written <= 0 || t.sampleRate <= 0 {
		return 0
	}
	if t.speakerFrames <= 0 {
		return t.samplesIntoAt(dst, written)
	}
	elapsed := int64(t.now().Sub(time.Unix(0, t.writeAt.Load())) * time.Duration(t.sampleRate) / time.Second)
	elapsed = max(0, elapsed)
	end := written - int64(t.speakerFrames) + elapsed
	// Never read past what has been written (a stall would otherwise walk the
	// window into stale ring-buffer data) nor before the start of the stream.
	end = max(0, min(end, written))
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
