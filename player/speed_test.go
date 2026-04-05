package player

import (
	"math"
	"sync/atomic"
	"testing"
)

func TestSpeedStreamerPassthroughAt1x(t *testing.T) {
	src := &fakeStreamer{val: [2]float64{0.5, -0.5}, count: 1024}
	var speed atomic.Uint64
	speed.Store(math.Float64bits(1.0))

	ss := newSpeedStreamer(src, &speed)

	samples := make([][2]float64, 128)
	n, ok := ss.Stream(samples)

	if n != 128 || !ok {
		t.Fatalf("Stream() = (%d, %v), want (128, true)", n, ok)
	}

	for i := range n {
		if math.Abs(samples[i][0]-0.5) > 1e-9 {
			t.Errorf("samples[%d][0] = %f, want 0.5", i, samples[i][0])
			break
		}
		if math.Abs(samples[i][1]-(-0.5)) > 1e-9 {
			t.Errorf("samples[%d][1] = %f, want -0.5", i, samples[i][1])
			break
		}
	}
}

func TestSpeedStreamerPassthroughAtZero(t *testing.T) {
	src := &fakeStreamer{val: [2]float64{0.3, 0.3}, count: 64}
	var speed atomic.Uint64
	speed.Store(math.Float64bits(0.0))

	ss := newSpeedStreamer(src, &speed)

	samples := make([][2]float64, 32)
	n, ok := ss.Stream(samples)

	if n != 32 || !ok {
		t.Fatalf("Stream() = (%d, %v), want (32, true)", n, ok)
	}
}

func TestSpeedStreamer2xProducesOutput(t *testing.T) {
	src := &sineStreamer{freq: 440, sr: 44100, count: 8192}
	var speed atomic.Uint64
	speed.Store(math.Float64bits(2.0))

	ss := newSpeedStreamer(src, &speed)

	samples := make([][2]float64, 4096)
	n, ok := ss.Stream(samples)

	if n == 0 {
		t.Fatal("Stream() at 2x speed returned 0 samples")
	}
	if !ok {
		t.Fatal("Stream() at 2x speed returned ok=false")
	}
}

func TestSpeedStreamerHalfSpeedProducesOutput(t *testing.T) {
	src := &sineStreamer{freq: 440, sr: 44100, count: 8192}
	var speed atomic.Uint64
	speed.Store(math.Float64bits(0.5))

	ss := newSpeedStreamer(src, &speed)

	samples := make([][2]float64, 4096)
	n, ok := ss.Stream(samples)

	if n == 0 {
		t.Fatal("Stream() at 0.5x speed returned 0 samples")
	}
	if !ok {
		t.Fatal("Stream() at 0.5x speed returned ok=false")
	}
}

func TestSpeedStreamerErr(t *testing.T) {
	src := &fakeStreamer{}
	var speed atomic.Uint64

	ss := newSpeedStreamer(src, &speed)
	if err := ss.Err(); err != nil {
		t.Errorf("Err() = %v, want nil", err)
	}
}

func TestTsAlphaTable(t *testing.T) {
	// Verify crossfade alpha table is properly initialized
	if tsAlpha[0] != 0.0 {
		t.Errorf("tsAlpha[0] = %f, want 0.0", tsAlpha[0])
	}
	lastIdx := len(tsAlpha) - 1
	expectedLast := float64(lastIdx) / float64(tsOvlp)
	if math.Abs(tsAlpha[lastIdx]-expectedLast) > 1e-9 {
		t.Errorf("tsAlpha[%d] = %f, want %f", lastIdx, tsAlpha[lastIdx], expectedLast)
	}

	// Should be monotonically increasing
	for i := 1; i < len(tsAlpha); i++ {
		if tsAlpha[i] <= tsAlpha[i-1] {
			t.Fatalf("tsAlpha[%d] (%f) <= tsAlpha[%d] (%f)", i, tsAlpha[i], i-1, tsAlpha[i-1])
		}
	}
}

// sineStreamer generates a sine wave for testing WSOLA
type sineStreamer struct {
	freq  float64
	sr    float64
	pos   int
	count int
}

func (s *sineStreamer) Stream(samples [][2]float64) (int, bool) {
	n := min(len(samples), s.count-s.pos)
	if n <= 0 {
		return 0, false
	}
	for i := range n {
		val := math.Sin(2 * math.Pi * s.freq * float64(s.pos+i) / s.sr)
		samples[i] = [2]float64{val, val}
	}
	s.pos += n
	return n, true
}

func (s *sineStreamer) Err() error { return nil }
