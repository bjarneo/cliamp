package player

import (
	"math"
	"sync/atomic"
	"testing"

	"github.com/gopxl/beep/v2"
)

// fakeStreamer produces constant sample values for testing.
type fakeStreamer struct {
	val   [2]float64
	count int
}

func (f *fakeStreamer) Stream(samples [][2]float64) (int, bool) {
	n := min(len(samples), f.count)
	for i := range n {
		samples[i] = f.val
	}
	f.count -= n
	return n, n > 0
}

func (f *fakeStreamer) Err() error { return nil }

func TestVolumeStreamerZeroDB(t *testing.T) {
	src := &fakeStreamer{val: [2]float64{0.5, -0.5}, count: 4}
	var vol atomic.Uint64
	vol.Store(math.Float64bits(0.0))
	var mono atomic.Bool

	vs := &volumeStreamer{
		s:        src,
		vol:      &vol,
		mono:     &mono,
		cachedDB: math.NaN(),
	}

	samples := make([][2]float64, 4)
	n, ok := vs.Stream(samples)
	if n != 4 || !ok {
		t.Fatalf("Stream() = (%d, %v), want (4, true)", n, ok)
	}

	for i := range n {
		if math.Abs(samples[i][0]-0.5) > 1e-9 {
			t.Errorf("samples[%d][0] = %f, want 0.5", i, samples[i][0])
		}
		if math.Abs(samples[i][1]-(-0.5)) > 1e-9 {
			t.Errorf("samples[%d][1] = %f, want -0.5", i, samples[i][1])
		}
	}
}

func TestVolumeStreamerNegativeDB(t *testing.T) {
	// -20 dB should attenuate by factor of 0.1
	src := &fakeStreamer{val: [2]float64{1.0, 1.0}, count: 4}
	var vol atomic.Uint64
	vol.Store(math.Float64bits(-20.0))
	var mono atomic.Bool

	vs := &volumeStreamer{
		s:        src,
		vol:      &vol,
		mono:     &mono,
		cachedDB: math.NaN(),
	}

	samples := make([][2]float64, 4)
	n, _ := vs.Stream(samples)

	expectedGain := math.Pow(10, -20.0/20) // 0.1
	for i := range n {
		if math.Abs(samples[i][0]-expectedGain) > 1e-9 {
			t.Errorf("samples[%d][0] = %f, want %f", i, samples[i][0], expectedGain)
		}
	}
}

func TestVolumeStreamerMono(t *testing.T) {
	src := &fakeStreamer{val: [2]float64{1.0, 0.0}, count: 4}
	var vol atomic.Uint64
	vol.Store(math.Float64bits(0.0))
	var mono atomic.Bool
	mono.Store(true)

	vs := &volumeStreamer{
		s:        src,
		vol:      &vol,
		mono:     &mono,
		cachedDB: math.NaN(),
	}

	samples := make([][2]float64, 4)
	n, _ := vs.Stream(samples)

	for i := range n {
		// Mono should average L and R: (1.0 + 0.0) / 2 = 0.5
		if math.Abs(samples[i][0]-0.5) > 1e-9 {
			t.Errorf("samples[%d][0] = %f, want 0.5", i, samples[i][0])
		}
		if math.Abs(samples[i][1]-0.5) > 1e-9 {
			t.Errorf("samples[%d][1] = %f, want 0.5", i, samples[i][1])
		}
	}
}

func TestVolumeStreamerEmptySource(t *testing.T) {
	src := &fakeStreamer{count: 0}
	var vol atomic.Uint64
	vol.Store(math.Float64bits(0.0))
	var mono atomic.Bool

	vs := &volumeStreamer{
		s:        src,
		vol:      &vol,
		mono:     &mono,
		cachedDB: math.NaN(),
	}

	samples := make([][2]float64, 4)
	n, ok := vs.Stream(samples)
	if n != 0 {
		t.Errorf("n = %d, want 0", n)
	}
	if ok {
		t.Error("ok should be false for empty source")
	}
}

func TestVolumeStreamerGainCaching(t *testing.T) {
	src := &fakeStreamer{val: [2]float64{1.0, 1.0}, count: 8}
	var vol atomic.Uint64
	vol.Store(math.Float64bits(0.0))
	var mono atomic.Bool

	vs := &volumeStreamer{
		s:        src,
		vol:      &vol,
		mono:     &mono,
		cachedDB: math.NaN(),
	}

	// First call at 0 dB
	samples := make([][2]float64, 4)
	vs.Stream(samples)
	if math.Abs(samples[0][0]-1.0) > 1e-9 {
		t.Errorf("at 0dB: samples[0][0] = %f, want 1.0", samples[0][0])
	}

	// Change volume to -6 dB
	vol.Store(math.Float64bits(-6.0))
	vs.Stream(samples)
	expectedGain := math.Pow(10, -6.0/20)
	if math.Abs(samples[0][0]-expectedGain) > 1e-4 {
		t.Errorf("at -6dB: samples[0][0] = %f, want ~%f", samples[0][0], expectedGain)
	}
}

func TestVolumeStreamerErr(t *testing.T) {
	src := &fakeStreamer{}
	var vol atomic.Uint64
	var mono atomic.Bool

	vs := &volumeStreamer{
		s:    src,
		vol:  &vol,
		mono: &mono,
	}

	if err := vs.Err(); err != nil {
		t.Errorf("Err() = %v, want nil", err)
	}
}

// Ensure fakeStreamer implements beep.Streamer at compile time.
var _ beep.Streamer = (*fakeStreamer)(nil)
