package player

import (
	"fmt"
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

func TestSearchBestOffsetMatchesExhaustive(t *testing.T) {
	tests := []struct {
		name     string
		expected int
		target   int
		inN      int
	}{
		{name: "middle", expected: tsSearch + 300, target: tsSearch + 437, inN: 2*tsSearch + tsWin + 600},
		{name: "lower bound", expected: 20, target: 7, inN: tsSearch + tsWin + 100},
		{name: "upper bound", expected: tsSearch + 300, target: 2*tsSearch + 290, inN: 2*tsSearch + tsWin + 290},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ss := searchFixture(tt.inN, tt.target)
			want := exhaustiveBestOffset(ss, tt.expected)
			got := ss.searchBestOffset(tt.expected)
			if got != want {
				t.Fatalf("searchBestOffset(%d) = %d, exhaustive = %d", tt.expected, got, want)
			}
			if got != tt.target {
				t.Fatalf("searchBestOffset(%d) = %d, want exact match %d", tt.expected, got, tt.target)
			}
		})
	}
}

func TestSearchBestOffsetBoundsAndFallback(t *testing.T) {
	ss := &speedStreamer{in: make([][2]float64, tsWin+100), inN: tsWin + 100}
	tests := []struct {
		name     string
		expected int
		want     int
	}{
		{name: "below range", expected: -50, want: 0},
		{name: "inside range", expected: 50, want: 50},
		{name: "above range", expected: tsWin + 500, want: 100},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ss.searchBestOffset(tt.expected); got != tt.want {
				t.Fatalf("searchBestOffset(%d) = %d, want %d", tt.expected, got, tt.want)
			}
		})
	}
}

func TestSearchBestOffsetSparsePhaseFallback(t *testing.T) {
	expected := tsSearch + 100
	target := expected + 173
	ss := &speedStreamer{in: make([][2]float64, 2*tsSearch+tsWin+300)}
	ss.inN = len(ss.in)
	for i := 1; i < tsOvlp; i += tsCoarse {
		v := float64(i + 1)
		ss.tail[i] = [2]float64{v, -v / 2}
		ss.in[target+i] = ss.tail[i]
	}
	for off := expected - tsSearch; off <= expected+tsSearch; off++ {
		if score := ss.offsetScore(off, tsCoarse); score != 0 {
			t.Fatalf("coarse score at %d = %v, want no usable candidates", off, score)
		}
	}
	want := exhaustiveBestOffset(ss, expected)
	if got := ss.searchBestOffset(expected); got != want || got != target {
		t.Fatalf("searchBestOffset(%d) = %d, exhaustive = %d, target = %d", expected, got, want, target)
	}
}

func TestSpeedStreamerMultiFrameTailContinuity(t *testing.T) {
	for _, ratio := range []float64{0.5, 2} {
		t.Run(fmt.Sprintf("%.1fx", ratio), func(t *testing.T) {
			src := &sineStreamer{freq: 437, sr: 44100, count: 200000}
			var speed atomic.Uint64
			speed.Store(math.Float64bits(ratio))
			ss := newSpeedStreamer(src, &speed)
			out := make([][2]float64, tsSeq)

			for frame := range 6 {
				wantFirst := ss.tail[0]
				n, ok := ss.Stream(out)
				if n != tsSeq || !ok {
					t.Fatalf("frame %d Stream() = (%d, %v), want (%d, true)", frame, n, ok, tsSeq)
				}
				if frame > 0 && out[0] != wantFirst {
					t.Fatalf("frame %d starts at %v, want prior tail %v", frame, out[0], wantFirst)
				}
				if !ss.tailValid {
					t.Fatalf("frame %d cleared valid WSOLA tail", frame)
				}
			}
		})
	}
}

func TestSearchBestOffsetNoAllocations(t *testing.T) {
	expected := tsSearch + 300
	ss := searchFixture(2*tsSearch+tsWin+600, expected+137)
	allocs := testing.AllocsPerRun(100, func() {
		if got := ss.searchBestOffset(expected); got != expected+137 {
			t.Fatalf("searchBestOffset(%d) = %d, want %d", expected, got, expected+137)
		}
	})
	if allocs != 0 {
		t.Fatalf("searchBestOffset() allocations = %v, want 0", allocs)
	}
}

func searchFixture(inN, target int) *speedStreamer {
	ss := &speedStreamer{in: make([][2]float64, inN), inN: inN}
	var state uint64 = 0x9e3779b97f4a7c15
	for i := range ss.in {
		state ^= state << 13
		state ^= state >> 7
		state ^= state << 17
		left := float64(int64(state>>11)%2000000)/1000000 - 1
		state ^= state << 13
		state ^= state >> 7
		state ^= state << 17
		right := float64(int64(state>>11)%2000000)/1000000 - 1
		ss.in[i] = [2]float64{left, right}
	}
	copy(ss.tail[:], ss.in[target:target+tsOvlp])
	return ss
}

func exhaustiveBestOffset(ss *speedStreamer, expected int) int {
	maxOff := max(0, ss.inN-tsWin)
	lo := min(max(0, expected-tsSearch), maxOff)
	hi := max(min(maxOff, expected+tsSearch), lo)
	bestOff := min(max(expected, lo), hi)
	bestScore := ss.offsetScore(bestOff, 1)
	for off := lo; off <= hi; off++ {
		score := ss.offsetScore(off, 1)
		if score > bestScore || (score == bestScore && score > 0 && off < bestOff) {
			bestOff, bestScore = off, score
		}
	}
	return bestOff
}

func BenchmarkSearchBestOffset(b *testing.B) {
	expected := tsSearch + 300
	ss := searchFixture(2*tsSearch+tsWin+600, expected+137)
	b.Run("coarse-to-fine", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			ss.searchBestOffset(expected)
		}
	})
	b.Run("exhaustive", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			exhaustiveBestOffset(ss, expected)
		}
	})
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
