package player

import (
	"math"
	"testing"

	"github.com/gopxl/beep/v2"
)

// hotBroadcastStreamer synthesizes a signal representative of a
// loudness-maximized Icecast/Shoutcast radio stream: several harmonically
// related tones within the source Nyquist range, brickwall-normalized to
// peak at exactly 1.0, as a limiter on the broadcast side would leave it.
type hotBroadcastStreamer struct {
	sr   float64
	pos  int
	norm float64
}

// newHotBroadcastStreamer normalizes the tone sum to an exact 1.0 peak by
// scanning one period of the lowest tone. Without it the raw sum peaks at
// ~1.20, and the source, not the resampler, would explain any overshoot.
func newHotBroadcastStreamer(sr float64) *hotBroadcastStreamer {
	s := &hotBroadcastStreamer{sr: sr}
	var peak float64
	for i := range int(sr) {
		peak = max(peak, math.Abs(s.raw(i)))
	}
	s.norm = 1 / peak
	return s
}

func (s *hotBroadcastStreamer) raw(n int) float64 {
	t := float64(n) / s.sr
	v := 0.0
	for _, hz := range []float64{440, 1200, 2500, 4200} {
		v += math.Sin(2 * math.Pi * hz * t)
	}
	return v / 3.2
}

func (s *hotBroadcastStreamer) sample(n int) float64 { return s.raw(n) * s.norm }

func (s *hotBroadcastStreamer) Stream(samples [][2]float64) (int, bool) {
	for i := range samples {
		v := s.sample(s.pos)
		samples[i] = [2]float64{v, v}
		s.pos++
	}
	return len(samples), true
}

func (s *hotBroadcastStreamer) Err() error { return nil }

// squareStreamer emits a full-scale square wave. Its discontinuities are the
// strongest ringing case for a windowed-sinc resampler while its peak stays
// exactly at full scale, so any output above 1.0 comes from the filter.
type squareStreamer struct {
	sr, hz float64
	pos    int
}

func (s *squareStreamer) sample(n int) float64 {
	if math.Mod(float64(n)*s.hz/s.sr, 1) < 0.5 {
		return 1
	}
	return -1
}

func (s *squareStreamer) Stream(samples [][2]float64) (int, bool) {
	for i := range samples {
		v := s.sample(s.pos)
		samples[i] = [2]float64{v, v}
		s.pos++
	}
	return len(samples), true
}

func (s *squareStreamer) Err() error { return nil }

func peakAbs(s beep.Streamer, n int) float64 {
	buf := make([][2]float64, n)
	got, _ := s.Stream(buf)
	var peak float64
	for _, fr := range buf[:got] {
		for _, v := range fr {
			if v < 0 {
				v = -v
			}
			if v > peak {
				peak = v
			}
		}
	}
	return peak
}

// TestResampleOvershootClips documents the underlying issue: resampling a
// full-scale signal with beep.Resample's windowed-sinc filter overshoots
// past [-1, 1] (Gibbs-phenomenon ringing) on a 22.05kHz stream, which the
// final PCM output stage then hard-clips even though no source sample
// exceeded full scale.
func TestResampleOvershootClips(t *testing.T) {
	for _, tc := range []struct {
		name string
		src  beep.Streamer
		from beep.SampleRate
		to   beep.SampleRate
	}{
		{"full-scale square 22k to 44k", &squareStreamer{sr: 22050, hz: 1000}, 22050, 44100},
	} {
		t.Run(tc.name, func(t *testing.T) {
			raw := beep.Resample(4, tc.from, tc.to, tc.src)
			if peak := peakAbs(raw, 4096); peak <= 1.0 {
				t.Fatalf("expected plain beep.Resample to overshoot 1.0, got peak=%v", peak)
			}
		})
	}
}

// TestResampleWithHeadroomAvoidsClipping verifies the fix: applying
// resampleHeadroomGain before resampling keeps the overshoot of a signal
// that peaks at full scale within [-1, 1], so the final output stage no
// longer hard-clips it.
func TestResampleWithHeadroomAvoidsClipping(t *testing.T) {
	for _, tc := range []struct {
		name string
		src  beep.Streamer
		from beep.SampleRate
		to   beep.SampleRate
	}{
		{"full-scale square 22k to 44k", &squareStreamer{sr: 22050, hz: 1000}, 22050, 44100},
		{"full-scale square 22k to 48k", &squareStreamer{sr: 22050, hz: 1000}, 22050, 48000},
		{"hot broadcast tones 22k to 44k", newHotBroadcastStreamer(22050), 22050, 44100},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := resampleWithHeadroom(4, tc.from, tc.to, tc.src)
			if peak := peakAbs(s, 4096); peak > 1.0 {
				t.Fatalf("resampleWithHeadroom still overshoots 1.0: peak=%v", peak)
			}
		})
	}
}

// TestResampleWithHeadroomNoopWhenRatesMatch ensures no gain is applied
// (and no beep.Resample wrapping happens) when source and target sample
// rates already match, so non-resampled playback loses no level.
func TestResampleWithHeadroomNoopWhenRatesMatch(t *testing.T) {
	src := &squareStreamer{sr: 44100, hz: 1000}
	unwrapped := &squareStreamer{sr: 44100, hz: 1000}
	s := resampleWithHeadroom(4, 44100, 44100, src)

	got := make([][2]float64, 4)
	s.Stream(got)
	want := make([][2]float64, 4)
	unwrapped.Stream(want)

	for i := range got {
		if got[i] != want[i] {
			t.Fatalf("expected unmodified passthrough sample %v, got %v", want[i], got[i])
		}
	}
}

// finiteRampStreamer emits exactly total samples of a linear ramp, then
// drains. A ramp is transparent to the resampler's polynomial filter: every
// output sample interpolates points lying on the same line, so output m
// reproduces the source value at position m*from/to — which makes per-sample
// position math directly verifiable.
type finiteRampStreamer struct {
	total int
	pos   int
	step  float64
}

func (s *finiteRampStreamer) Stream(samples [][2]float64) (int, bool) {
	n := 0
	for i := range samples {
		if s.pos >= s.total {
			return n, n > 0
		}
		v := float64(s.pos) * s.step
		samples[i] = [2]float64{v, v}
		s.pos++
		n++
	}
	return n, true
}

func (s *finiteRampStreamer) Err() error { return nil }

// drainStreamer reads s until it stops producing samples and returns every
// stereo pair it emitted.
func drainStreamer(s beep.Streamer, bufSize int) [][2]float64 {
	var out [][2]float64
	buf := make([][2]float64, bufSize)
	for {
		n, ok := s.Stream(buf)
		out = append(out, buf[:n]...)
		if !ok {
			return out
		}
	}
}

// TestResampleOvershootClips44100To48000 documents that the overshoot problem
// is not specific to low-rate broadcast sources: on the Spotify path too —
// 44100Hz material meeting a 48000Hz speaker rate — beep.Resample's filter
// rings a full-scale signal past [-1, 1], which the final int16 output stage
// would hard-clip.
func TestResampleOvershootClips44100To48000(t *testing.T) {
	for _, tc := range []struct {
		name string
		src  beep.Streamer
	}{
		{"full-scale square 44.1k to 48k", &squareStreamer{sr: 44100, hz: 1000}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			raw := beep.Resample(4, 44100, 48000, tc.src)
			if peak := peakAbs(raw, 4096); peak <= 1.0 {
				t.Fatalf("expected plain beep.Resample to overshoot 1.0, got peak=%v", peak)
			}
		})
	}
}

// TestResampleWithHeadroom44100To48000Length locks the output-length contract
// on the Spotify rate pair (44100Hz source, 48000Hz speaker rate): for an
// n-sample input the resampler emits output positions while int(pos*from/to)
// still indexes valid input — exactly ceil(n*to/from) samples, no more, no
// fewer.
func TestResampleWithHeadroom44100To48000Length(t *testing.T) {
	for _, tc := range []struct {
		name  string
		input int
		want  int
	}{
		{"one second", 44100, 48000},
		{"ratio-exact", 147, 160},
		{"non-integer ratio", 1000, 1089},
		{"under one resampler buffer", 512, 558},
		{"single sample", 1, 2},
		{"empty input", 0, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			src := &finiteRampStreamer{total: tc.input, step: 1e-6}
			got := drainStreamer(resampleWithHeadroom(4, 44100, 48000, src), 512)
			if len(got) != tc.want {
				t.Fatalf("resampled %d samples to %d, want %d", tc.input, len(got), tc.want)
			}
		})
	}
}

// TestResampleWithHeadroom44100To48000Position verifies output sample m is
// interpolated at source position m*44100/48000: the linear ramp survives the
// filter unchanged, so a value off the line means the resampler read the
// wrong input position (a phase/alignment regression). The expected values
// include resampleHeadroomGain, locking the gain in as well.
func TestResampleWithHeadroom44100To48000Position(t *testing.T) {
	const (
		input = 4410
		step  = 1e-4
	)
	src := &finiteRampStreamer{total: input, step: step}
	got := drainStreamer(resampleWithHeadroom(4, 44100, 48000, src), 333)

	wantLen := int(math.Ceil(input * 48000.0 / 44100.0))
	if len(got) != wantLen {
		t.Fatalf("output length = %d, want %d", len(got), wantLen)
	}
	const eps = 1e-6
	for m, fr := range got {
		wantPos := float64(m) * 44100.0 / 48000.0
		want := resampleHeadroomGain * wantPos * step
		for c := range 2 {
			if math.Abs(fr[c]-want) > eps {
				t.Fatalf("output[%d][%d] = %v, want %v (source position %v)", m, c, fr[c], want, wantPos)
			}
		}
	}
}

// TestResampleWithHeadroom44100To48000NoClip locks the no-clip invariant on
// the Spotify rate pair: material that already peaks at full scale at
// 44100Hz must stay within [-1, 1] after headroom + resample to 48000Hz, or
// the final output stage would clip it.
func TestResampleWithHeadroom44100To48000NoClip(t *testing.T) {
	for _, tc := range []struct {
		name string
		src  beep.Streamer
	}{
		{"full-scale square 44.1k to 48k", &squareStreamer{sr: 44100, hz: 1000}},
		{"hot broadcast tones 44.1k to 48k", newHotBroadcastStreamer(44100)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := resampleWithHeadroom(4, 44100, 48000, tc.src)
			if peak := peakAbs(s, 4096); peak > 1.0 {
				t.Fatalf("resampleWithHeadroom still overshoots 1.0: peak=%v", peak)
			}
		})
	}
}
