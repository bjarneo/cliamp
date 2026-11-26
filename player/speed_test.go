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
	// When coarse search finds no candidates, return expected without an exhaustive scan.
	if got := ss.searchBestOffset(expected); got != expected {
		t.Fatalf("searchBestOffset(%d) = %d, want fallback to expected %d", expected, got, expected)
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

func TestSearchBestOffsetSilentTailReturnsExpected(t *testing.T) {
	tests := []struct {
		name     string
		tailVal  float64
		expected int
		want     int
	}{
		{name: "zero tail inside range", tailVal: 0, expected: tsSearch + 100, want: tsSearch + 100},
		{name: "below energy threshold", tailVal: 1e-7, expected: tsSearch + 50, want: tsSearch + 50},
		{name: "clamped below zero", tailVal: 0, expected: -10, want: 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ss := searchFixture(2*tsSearch+tsWin+300, tsSearch+200)
			for i := range ss.tail {
				ss.tail[i] = [2]float64{tt.tailVal, tt.tailVal}
			}
			if got := ss.searchBestOffset(tt.expected); got != tt.want {
				t.Fatalf("searchBestOffset(%d) = %d, want %d", tt.expected, got, tt.want)
			}
		})
	}
}

func TestSpeedStreamerOutputCompactionNoSteadyStateAllocs(t *testing.T) {
	for _, ratio := range []float64{0.5, 1.5, 2.0} {
		t.Run(fmt.Sprintf("%.1fx", ratio), func(t *testing.T) {
			src := &sineStreamer{freq: 440, sr: 44100, count: 1_000_000}
			var speed atomic.Uint64
			speed.Store(math.Float64bits(ratio))
			ss := newSpeedStreamer(src, &speed)
			if ss.in != nil || ss.out != nil {
				t.Fatalf("newSpeedStreamer allocated WSOLA buffers eagerly: in=%v out=%v", len(ss.in), len(ss.out))
			}
			out := make([][2]float64, 1024)

			// Warm up the first frames so lazy WSOLA and energy buffers allocate once.
			for range 4 {
				if n, ok := ss.Stream(out); n != len(out) || !ok {
					t.Fatalf("warmup Stream() = (%d, %v)", n, ok)
				}
			}
			initialCap := cap(ss.out)
			allocs := testing.AllocsPerRun(20, func() {
				if n, ok := ss.Stream(out); n != len(out) || !ok {
					t.Fatalf("Stream() = (%d, %v)", n, ok)
				}
			})
			if allocs != 0 {
				t.Fatalf("steady-state Stream allocations = %v, want 0", allocs)
			}
			if cap(ss.out) != initialCap {
				t.Fatalf("out buffer grew from %d to %d", initialCap, cap(ss.out))
			}
		})
	}
}

func TestSpeedStreamerLazyBuffersAnd1xFastPath(t *testing.T) {
	var speed atomic.Uint64
	speed.Store(math.Float64bits(1.0))
	src := &fakeStreamer{val: [2]float64{0.25, -0.75}, count: 100000}
	ss := newSpeedStreamer(src, &speed)
	out := make([][2]float64, 512)

	allocs := testing.AllocsPerRun(20, func() {
		n, ok := ss.Stream(out)
		if n != len(out) || !ok {
			t.Fatalf("1.0x Stream() = (%d, %v), want (%d, true)", n, ok, len(out))
		}
	})
	if allocs != 0 {
		t.Fatalf("1.0x Stream() allocations = %v, want 0", allocs)
	}
	if ss.in != nil || ss.out != nil || ss.inEnergy != nil || ss.coarseEnergy != nil {
		t.Fatal("1.0x playback allocated WSOLA buffers")
	}

	// Switch to 1.5x, then back to 1.0x and verify passthrough drains cleanly.
	speed.Store(math.Float64bits(1.5))
	if n, ok := ss.Stream(out); n != len(out) || !ok {
		t.Fatalf("1.5x Stream() = (%d, %v), want (%d, true)", n, ok, len(out))
	}
	if ss.in == nil || ss.out == nil {
		t.Fatal("non-1.0x playback did not allocate WSOLA buffers")
	}

	speed.Store(math.Float64bits(1.0))
	for range 20 {
		if n, ok := ss.Stream(out); n != len(out) || !ok {
			t.Fatalf("drain back to 1.0x Stream() = (%d, %v)", n, ok)
		}
	}
	if ss.inN != 0 || ss.outRd != ss.outWr || ss.tailValid {
		t.Fatalf("passthrough did not return to fast-path state: inN=%d outRd=%d outWr=%d tailValid=%v",
			ss.inN, ss.outRd, ss.outWr, ss.tailValid)
	}
}

func TestSpeedStreamerReset(t *testing.T) {
	src := &sineStreamer{freq: 440, sr: 44100, count: 100000}
	var speed atomic.Uint64
	speed.Store(math.Float64bits(1.5))
	ss := newSpeedStreamer(src, &speed)
	out := make([][2]float64, 1024)
	if n, ok := ss.Stream(out); n != len(out) || !ok {
		t.Fatalf("Stream() = (%d, %v)", n, ok)
	}
	if !ss.tailValid || ss.inN == 0 || ss.outWr == 0 {
		t.Fatalf("expected populated WSOLA state before Reset: tailValid=%v inN=%d outWr=%d", ss.tailValid, ss.inN, ss.outWr)
	}

	ss.Reset()
	if ss.tailValid || ss.inN != 0 || ss.inPos != 0 || ss.outRd != 0 || ss.outWr != 0 {
		t.Fatalf("Reset left state: tailValid=%v inN=%d inPos=%v outRd=%d outWr=%d", ss.tailValid, ss.inN, ss.inPos, ss.outRd, ss.outWr)
	}
	for i, v := range ss.tail {
		if v != ([2]float64{}) {
			t.Fatalf("tail[%d] = %v after Reset, want zero", i, v)
		}
	}

	// After Reset, Stream reads only samples from the new source.
	ss.s = &fakeStreamer{val: [2]float64{0.6, -0.4}, count: 20000}
	if n, ok := ss.Stream(out); n != len(out) || !ok {
		t.Fatalf("Stream() after Reset = (%d, %v)", n, ok)
	}
	for i := range out {
		if out[i] != ([2]float64{0.6, -0.4}) {
			t.Fatalf("out[%d] after Reset = %v, want [0.6 -0.4]", i, out[i])
		}
	}
}

func TestWSOLAFrameClampsShortTail(t *testing.T) {
	tests := []struct {
		name      string
		totalIn   int
		wantValid int
	}{
		{name: "exact sequence length", totalIn: tsSeq, wantValid: 0},
		{name: "partial tail near EOF", totalIn: tsSeq + 100, wantValid: 100},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var speed atomic.Uint64
			speed.Store(math.Float64bits(1.5))
			ss := newSpeedStreamer(&fakeStreamer{val: [2]float64{0.25, -0.25}, count: tt.totalIn}, &speed)
			ss.ensureBuffers()
			// Pre-fill stale values beyond totalIn to verify clamping and zeroing.
			for i := range ss.in {
				ss.in[i] = [2]float64{9, 9}
			}
			for i := range ss.tail {
				ss.tail[i] = [2]float64{7, 7}
			}
			if !ss.wsolaFrame(1.5) {
				t.Fatal("wsolaFrame() = false, want true")
			}
			for i := range tt.wantValid {
				if ss.tail[i] != ([2]float64{0.25, -0.25}) {
					t.Fatalf("tail[%d] = %v, want [0.25 -0.25]", i, ss.tail[i])
				}
			}
			for i := tt.wantValid; i < tsOvlp; i++ {
				if ss.tail[i] != ([2]float64{}) {
					t.Fatalf("tail[%d] = %v, want zeroed remainder", i, ss.tail[i])
				}
			}
		})
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

func BenchmarkSpeed1x(b *testing.B) {
	var speed atomic.Uint64
	speed.Store(math.Float64bits(1.0))
	ss := newSpeedStreamer(&noopStreamer{}, &speed)
	buf := make([][2]float64, 1024)
	b.ReportAllocs()
	for b.Loop() {
		ss.Stream(buf)
	}
}

func BenchmarkSpeedNew(b *testing.B) {
	var speed atomic.Uint64
	speed.Store(math.Float64bits(1.0))
	src := &noopStreamer{}
	b.ReportAllocs()
	for b.Loop() {
		_ = newSpeedStreamer(src, &speed)
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
