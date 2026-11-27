package player

import (
	"math"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/gopxl/beep/v2"
)

type sequenceStreamer struct {
	samples [][2]float64
	pos     int
}

func (s *sequenceStreamer) Stream(dst [][2]float64) (int, bool) {
	n := copy(dst, s.samples[s.pos:])
	s.pos += n
	return n, s.pos < len(s.samples)
}

func (*sequenceStreamer) Err() error { return nil }

func TestTapPreservesStereoAndMonoMixAcrossWraparound(t *testing.T) {
	samples := [][2]float64{
		{1, -1},
		{0.2, 0.4},
		{0.3, 0.5},
		{0.4, 0.6},
		{0.5, 0.7},
		{0.6, 0.8},
	}
	tap := newTap(&sequenceStreamer{samples: samples}, 4, 44100, 0)

	for range 2 {
		out := make([][2]float64, 3)
		if n, _ := tap.Stream(out); n != len(out) {
			t.Fatalf("Stream() wrote %d frames, want %d", n, len(out))
		}
	}

	stereo := make([][2]float64, 4)
	if n := tap.StereoSamplesInto(stereo); n != len(stereo) {
		t.Fatalf("StereoSamplesInto() = %d, want %d", n, len(stereo))
	}
	wantStereo := samples[2:]
	if !reflect.DeepEqual(stereo, wantStereo) {
		t.Fatalf("StereoSamplesInto() = %v, want %v", stereo, wantStereo)
	}

	mono := make([]float64, 4)
	if n := tap.SamplesInto(mono); n != len(mono) {
		t.Fatalf("SamplesInto() = %d, want %d", n, len(mono))
	}
	wantMono := []float64{0.4, 0.5, 0.6, 0.7}
	if !reflect.DeepEqual(mono, wantMono) {
		t.Fatalf("SamplesInto() = %v, want %v", mono, wantMono)
	}
}

func TestTapWaveformSamplesAdvanceBetweenWrites(t *testing.T) {
	samples := make([][2]float64, 8)
	for i := range samples {
		samples[i] = [2]float64{float64(i), float64(i)}
	}

	now := time.Unix(1, 0)
	tap := newTap(&sequenceStreamer{samples: samples}, 16, 10, 8)
	tap.now = func() time.Time { return now }
	if n, _ := tap.Stream(make([][2]float64, len(samples))); n != len(samples) {
		t.Fatalf("Stream() wrote %d frames, want %d", n, len(samples))
	}

	buf := make([]float64, 2)
	now = now.Add(300 * time.Millisecond)
	if n := tap.WaveformSamplesInto(buf); n != len(buf) {
		t.Fatalf("WaveformSamplesInto() = %d, want %d", n, len(buf))
	}
	if want := []float64{1, 2}; !reflect.DeepEqual(buf, want) {
		t.Fatalf("WaveformSamplesInto() = %v, want %v", buf, want)
	}

	now = now.Add(400 * time.Millisecond)
	if n := tap.WaveformSamplesInto(buf); n != len(buf) {
		t.Fatalf("WaveformSamplesInto() = %d, want %d", n, len(buf))
	}
	if want := []float64{5, 6}; !reflect.DeepEqual(buf, want) {
		t.Fatalf("WaveformSamplesInto() = %v, want %v", buf, want)
	}
}

// TestTapWaveformAdvancesPastLastWriteSize covers the case that made raw
// visualizers stutter: the backend refills its buffer in bursts, so the last
// Stream call can be much shorter than the buffer it filled. Anchoring the read
// position to that call froze the waveform until the next burst — here, both
// reads would return the same frames.
func TestTapWaveformAdvancesPastLastWriteSize(t *testing.T) {
	samples := make([][2]float64, 8)
	for i := range samples {
		samples[i] = [2]float64{float64(i), float64(i)}
	}

	now := time.Unix(1, 0)
	// 10 Hz, an 8-frame backend buffer: 800 ms of audio in flight.
	tap := newTap(&sequenceStreamer{samples: samples}, 16, 10, 8)
	tap.now = func() time.Time { return now }

	// A long refill followed by a short tail, as a bursty backend does.
	if n, _ := tap.Stream(make([][2]float64, 6)); n != 6 {
		t.Fatalf("Stream() wrote %d frames, want 6", n)
	}
	if n, _ := tap.Stream(make([][2]float64, 2)); n != 2 {
		t.Fatalf("Stream() wrote %d frames, want 2", n)
	}

	buf := make([]float64, 2)
	now = now.Add(300 * time.Millisecond)
	if n := tap.WaveformSamplesInto(buf); n != len(buf) {
		t.Fatalf("WaveformSamplesInto() = %d, want %d", n, len(buf))
	}
	if want := []float64{1, 2}; !reflect.DeepEqual(buf, want) {
		t.Fatalf("WaveformSamplesInto() at +300ms = %v, want %v", buf, want)
	}

	// 400 ms later the window must have moved on, even though the last write
	// was only 2 frames long.
	now = now.Add(400 * time.Millisecond)
	if n := tap.WaveformSamplesInto(buf); n != len(buf) {
		t.Fatalf("WaveformSamplesInto() = %d, want %d", n, len(buf))
	}
	if want := []float64{5, 6}; !reflect.DeepEqual(buf, want) {
		t.Fatalf("WaveformSamplesInto() at +700ms = %v, want %v", buf, want)
	}
}

// TestTapWaveformDoesNotReadPastWritten guards the clamp: if the backend stalls,
// the window must stop at the newest written frame instead of walking into
// stale ring-buffer data.
func TestTapWaveformDoesNotReadPastWritten(t *testing.T) {
	samples := make([][2]float64, 8)
	for i := range samples {
		samples[i] = [2]float64{float64(i), float64(i)}
	}

	now := time.Unix(1, 0)
	tap := newTap(&sequenceStreamer{samples: samples}, 16, 10, 8)
	tap.now = func() time.Time { return now }
	if n, _ := tap.Stream(make([][2]float64, 8)); n != 8 {
		t.Fatalf("Stream() wrote %d frames, want 8", n)
	}

	buf := make([]float64, 2)
	now = now.Add(30 * time.Second)
	if n := tap.WaveformSamplesInto(buf); n != len(buf) {
		t.Fatalf("WaveformSamplesInto() = %d, want %d", n, len(buf))
	}
	if want := []float64{6, 7}; !reflect.DeepEqual(buf, want) {
		t.Fatalf("WaveformSamplesInto() after stall = %v, want %v", buf, want)
	}
}

// TestTapRingFramesLeavesRoomForTheAnalysisWindow pins the ring sizing. The
// waveform position trails the newest frame by the whole backend buffer, so a
// ring sized to that buffer alone would have already overwritten the oldest end
// of the window being read.
func TestTapRingFramesLeavesRoomForTheAnalysisWindow(t *testing.T) {
	for _, tc := range []struct {
		name          string
		speakerFrames int
		want          int
	}{
		{name: "250ms at 44.1kHz", speakerFrames: 11025, want: 11025 + maxAnalysisWindow},
		{name: "50ms at 44.1kHz", speakerFrames: 2205, want: 2205 + maxAnalysisWindow},
		{name: "none", speakerFrames: 0, want: 4096},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := tapRingFrames(tc.speakerFrames); got != tc.want {
				t.Errorf("tapRingFrames(%d) = %d, want %d", tc.speakerFrames, got, tc.want)
			}
			if got := tapRingFrames(tc.speakerFrames); got < tc.speakerFrames+maxAnalysisWindow && tc.speakerFrames > 0 {
				t.Errorf("tapRingFrames(%d) = %d, leaves no room for a %d-frame window",
					tc.speakerFrames, got, maxAnalysisWindow)
			}
		})
	}
}

// TestTapWaveformReadsAudiblePositionAfterRefill checks the read against a ring
// sized the way the player sizes it: right after a refill the window must show
// the frames that are audible now, which are speakerFrames back, not the ones
// just handed to the backend.
func TestTapWaveformReadsAudiblePositionAfterRefill(t *testing.T) {
	const (
		speakerFrames = 8
		window        = 2
	)
	samples := make([][2]float64, speakerFrames+window)
	for i := range samples {
		samples[i] = [2]float64{float64(i), float64(i)}
	}

	now := time.Unix(1, 0)
	tap := newTap(&sequenceStreamer{samples: samples}, speakerFrames+window, 10, speakerFrames)
	tap.now = func() time.Time { return now }
	if n, _ := tap.Stream(make([][2]float64, len(samples))); n != len(samples) {
		t.Fatalf("Stream() wrote %d frames, want %d", n, len(samples))
	}

	buf := make([]float64, window)
	if n := tap.WaveformSamplesInto(buf); n != len(buf) {
		t.Fatalf("WaveformSamplesInto() = %d, want %d", n, len(buf))
	}
	// written = 10, speakerFrames = 8, elapsed = 0 -> window ends at frame 2.
	if want := []float64{0, 1}; !reflect.DeepEqual(buf, want) {
		t.Fatalf("WaveformSamplesInto() = %v, want %v", buf, want)
	}
}

func TestTapLargeWriteAndConcurrentReadsRaceFree(t *testing.T) {
	// Write larger than ring capacity preserves the newest frames in order.
	samples := [][2]float64{
		{0, 0}, {1, 1}, {2, 2}, {3, 3}, {4, 4}, {5, 5}, {6, 6},
	}
	tp := newTap(&sequenceStreamer{samples: samples}, 4, 44100, 2)
	out := make([][2]float64, len(samples))
	if n, _ := tp.Stream(out); n != len(samples) {
		t.Fatalf("Stream() = %d, want %d", n, len(samples))
	}
	stereo := make([][2]float64, 4)
	if n := tp.StereoSamplesInto(stereo); n != 4 {
		t.Fatalf("StereoSamplesInto() = %d, want 4", n)
	}
	want := [][2]float64{{3, 3}, {4, 4}, {5, 5}, {6, 6}}
	if !reflect.DeepEqual(stereo, want) {
		t.Fatalf("StereoSamplesInto() = %v, want %v", stereo, want)
	}

	// Concurrent reads and writes must stay race-free under -race.
	liveTap := newTap(&fakeStreamer{val: [2]float64{0.5, -0.5}, count: 100000}, 128, 44100, 32)
	var wg sync.WaitGroup
	wg.Add(4)
	go func() {
		defer wg.Done()
		buf := make([][2]float64, 64)
		for range 300 {
			liveTap.Stream(buf)
		}
	}()
	go func() {
		defer wg.Done()
		buf := make([]float64, 64)
		for range 300 {
			liveTap.SamplesInto(buf)
		}
	}()
	go func() {
		defer wg.Done()
		buf := make([]float64, 64)
		for range 300 {
			liveTap.WaveformSamplesInto(buf)
		}
	}()
	go func() {
		defer wg.Done()
		buf := make([][2]float64, 64)
		for range 300 {
			liveTap.StereoSamplesInto(buf)
		}
	}()
	wg.Wait()
}

func TestDurationAndFrameConversionsNoOverflow(t *testing.T) {
	tests := []struct {
		name      string
		dur       time.Duration
		sr        int
		wantFrame int64
	}{
		{name: "zero duration", dur: 0, sr: 44100, wantFrame: 0},
		{name: "negative duration", dur: -time.Second, sr: 44100, wantFrame: 0},
		{name: "fractional second", dur: 5 * time.Millisecond, sr: 44100, wantFrame: 220},
		{name: "100 hours at 192kHz", dur: 100 * time.Hour, sr: 192000, wantFrame: 100 * 3600 * 192000},
		{name: "100 days at 192kHz", dur: 100 * 24 * time.Hour, sr: 192000, wantFrame: 100 * 86400 * 192000},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := durationToFrames(tt.dur, tt.sr); got != tt.wantFrame {
				t.Fatalf("durationToFrames(%v, %d) = %d, want %d", tt.dur, tt.sr, got, tt.wantFrame)
			}
			if tt.wantFrame > 0 {
				back := framesToDuration(tt.wantFrame, beep.SampleRate(tt.sr))
				if back <= 0 {
					t.Fatalf("framesToDuration(%d, %d) = %v, want positive duration", tt.wantFrame, tt.sr, back)
				}
				if diff := tt.dur - back; diff < 0 || diff >= time.Second/time.Duration(tt.sr)+time.Nanosecond {
					t.Fatalf("round-trip duration = %v, want within 1 frame of %v", back, tt.dur)
				}
			}
		})
	}

	// Verify WaveformSamplesInto does not overflow after a multi-day stall.
	now := time.Unix(1, 0)
	tp := newTap(&sequenceStreamer{samples: [][2]float64{{1, 1}, {2, 2}, {3, 3}, {4, 4}}}, 8, 192000, 2)
	tp.now = func() time.Time { return now }
	tp.Stream(make([][2]float64, 4))
	now = now.Add(10 * 24 * time.Hour)
	buf := make([]float64, 2)
	if n := tp.WaveformSamplesInto(buf); n != 2 {
		t.Fatalf("WaveformSamplesInto() = %d, want 2", n)
	}
	if !reflect.DeepEqual(buf, []float64{3, 4}) {
		t.Fatalf("WaveformSamplesInto() after 10-day stall = %v, want [3 4]", buf)
	}
	if got := durationToFrames(time.Duration(math.MaxInt64), 192000); got <= 0 {
		t.Fatalf("durationToFrames(MaxInt64, 192000) = %d, want positive", got)
	}
}

func TestTapSetEnabledSkipsCaptureAndClock(t *testing.T) {
	nowCalls := 0
	tp := newTap(&fakeStreamer{val: [2]float64{0.4, -0.2}, count: 100}, 16, 44100, 4)
	tp.now = func() time.Time {
		nowCalls++
		return time.Unix(1, 0)
	}

	out := make([][2]float64, 4)
	// Enabled by default.
	if n, ok := tp.Stream(out); n != 4 || !ok {
		t.Fatalf("enabled Stream() = (%d, %v), want (4, true)", n, ok)
	}
	if nowCalls != 1 || tp.written != 4 {
		t.Fatalf("after enabled Stream: nowCalls=%d written=%d, want 1, 4", nowCalls, tp.written)
	}

	// Disabled skips both ring writes and clock queries while passing audio through.
	tp.SetEnabled(false)
	if n, ok := tp.Stream(out); n != 4 || !ok {
		t.Fatalf("disabled Stream() = (%d, %v), want (4, true)", n, ok)
	}
	if out[0] != ([2]float64{0.4, -0.2}) {
		t.Fatalf("disabled Stream() altered audio: got %v", out[0])
	}
	if nowCalls != 1 || tp.written != 4 {
		t.Fatalf("disabled Stream() touched capture state: nowCalls=%d written=%d, want 1, 4", nowCalls, tp.written)
	}

	// SetEnabled(true) resumes capture.
	tp.SetEnabled(true)
	if n, ok := tp.Stream(out); n != 4 || !ok {
		t.Fatalf("re-enabled Stream() = (%d, %v), want (4, true)", n, ok)
	}
	if nowCalls != 2 || tp.written != 8 {
		t.Fatalf("after re-enabled Stream: nowCalls=%d written=%d, want 2, 8", nowCalls, tp.written)
	}
}

func TestTapSamplesIntoZeroSteadyStateAllocs(t *testing.T) {
	tp := newTap(&levelDecoder{level: 0.5, left: -1}, 4096, 44100, 512)
	tp.Stream(make([][2]float64, 4096))
	dst := make([]float64, 1024)

	allocsSamples := testing.AllocsPerRun(50, func() {
		if n := tp.SamplesInto(dst); n != len(dst) {
			t.Fatalf("SamplesInto() = %d, want %d", n, len(dst))
		}
	})
	if allocsSamples != 0 {
		t.Fatalf("SamplesInto() allocations = %v, want 0", allocsSamples)
	}

	allocsWave := testing.AllocsPerRun(50, func() {
		if n := tp.WaveformSamplesInto(dst); n != len(dst) {
			t.Fatalf("WaveformSamplesInto() = %d, want %d", n, len(dst))
		}
	})
	if allocsWave != 0 {
		t.Fatalf("WaveformSamplesInto() allocations = %v, want 0", allocsWave)
	}
}

func BenchmarkTap(b *testing.B) {
	buf := make([][2]float64, 1024)
	dst := make([]float64, 2048)

	b.Run("Stream_enabled", func(b *testing.B) {
		tp := newTap(&noopStreamer{}, 8192, 44100, 2048)
		b.ReportAllocs()
		for b.Loop() {
			tp.Stream(buf)
		}
	})
	b.Run("Stream_disabled", func(b *testing.B) {
		tp := newTap(&noopStreamer{}, 8192, 44100, 2048)
		tp.SetEnabled(false)
		b.ReportAllocs()
		for b.Loop() {
			tp.Stream(buf)
		}
	})
	b.Run("SamplesInto", func(b *testing.B) {
		tp := newTap(&levelDecoder{level: 0.5, left: -1}, 8192, 44100, 2048)
		tp.Stream(make([][2]float64, 4096))
		b.ReportAllocs()
		for b.Loop() {
			tp.SamplesInto(dst)
		}
	})
	b.Run("WaveformSamplesInto", func(b *testing.B) {
		tp := newTap(&levelDecoder{level: 0.5, left: -1}, 8192, 44100, 2048)
		tp.Stream(make([][2]float64, 4096))
		b.ReportAllocs()
		for b.Loop() {
			tp.WaveformSamplesInto(dst)
		}
	})
}
