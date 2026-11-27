package player

import (
	"errors"
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

type stereoSineStreamer struct {
	freqL, freqR float64
	sr           float64
	pos          int
	count        int // negative for infinite
}

func (s *stereoSineStreamer) Stream(samples [][2]float64) (int, bool) {
	n := len(samples)
	if s.count >= 0 {
		n = min(n, s.count-s.pos)
		if n <= 0 {
			return 0, false
		}
	}
	for i := range n {
		t := float64(s.pos+i) / s.sr
		samples[i][0] = math.Sin(2 * math.Pi * s.freqL * t)
		samples[i][1] = math.Cos(2 * math.Pi * s.freqR * t)
	}
	s.pos += n
	return n, true
}

func (*stereoSineStreamer) Err() error { return nil }

type errStreamer struct {
	err error
}

func (*errStreamer) Stream([][2]float64) (int, bool) { return 0, false }
func (e *errStreamer) Err() error                    { return e.err }

func TestResampleDCGain(t *testing.T) {
	tests := []struct {
		name    string
		quality int
		from    beep.SampleRate
		to      beep.SampleRate
	}{
		{name: "exact 44.1k to 48k q4", quality: 4, from: 44100, to: 48000},
		{name: "exact 96k to 48k q4", quality: 4, from: 96000, to: 48000},
		{name: "exact 48k to 44.1k q2", quality: 2, from: 48000, to: 44100},
		{name: "interpolated 22.05k to 48k q4", quality: 4, from: 22050, to: 48000},
		{name: "interpolated 32k to 44.1k q1", quality: 1, from: 32000, to: 44100},
		{name: "quality 8 44.1k to 48k", quality: 8, from: 44100, to: 48000},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			src := &levelDecoder{level: 1.0, left: -1}
			r := resampleWithHeadroom(tt.quality, tt.from, tt.to, src)
			buf := make([][2]float64, 1024)
			n, ok := r.Stream(buf)
			if n != len(buf) || !ok {
				t.Fatalf("Stream() = (%d, %v), want (%d, true)", n, ok, len(buf))
			}
			// Skip edge transient of at most 128 output frames.
			for i := 256; i < n; i++ {
				if diff := math.Abs(buf[i][0] - resampleHeadroomGain); diff > 1e-12 {
					t.Fatalf("sample %d left = %.15f, want %.15f (diff %.3e)", i, buf[i][0], resampleHeadroomGain, diff)
				}
				if diff := math.Abs(buf[i][1] - resampleHeadroomGain); diff > 1e-12 {
					t.Fatalf("sample %d right = %.15f, want %.15f (diff %.3e)", i, buf[i][1], resampleHeadroomGain, diff)
				}
			}
		})
	}
}

func TestResampleSinePhaseAndSNR(t *testing.T) {
	tests := []struct {
		name     string
		quality  int
		from     beep.SampleRate
		to       beep.SampleRate
		minSNRdB float64
	}{
		{name: "44.1k to 48k q4", quality: 4, from: 44100, to: 48000, minSNRdB: 85},
		{name: "96k to 48k q4", quality: 4, from: 96000, to: 48000, minSNRdB: 85},
		{name: "48k to 44.1k q4", quality: 4, from: 48000, to: 44100, minSNRdB: 85},
		{name: "22.05k to 48k interpolated q4", quality: 4, from: 22050, to: 48000, minSNRdB: 80},
		{name: "32k to 44.1k interpolated q4", quality: 4, from: 32000, to: 44100, minSNRdB: 80},
		{name: "44.1k to 48k q1", quality: 1, from: 44100, to: 48000, minSNRdB: 55},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			const freqL = 440.0
			const freqR = 880.0
			src := &stereoSineStreamer{freqL: freqL, freqR: freqR, sr: float64(tt.from), count: -1}
			r := resampleWithHeadroom(tt.quality, tt.from, tt.to, src)

			const totalOut = 4096
			buf := make([][2]float64, totalOut)
			n, ok := r.Stream(buf)
			if n != totalOut || !ok {
				t.Fatalf("Stream() = (%d, %v), want (%d, true)", n, ok, totalOut)
			}

			var sigL, errL, sigR, errR float64
			for i := 256; i < totalOut-256; i++ {
				tm := float64(i) / float64(tt.to)
				wantL := resampleHeadroomGain * math.Sin(2*math.Pi*freqL*tm)
				wantR := resampleHeadroomGain * math.Cos(2*math.Pi*freqR*tm)
				dL := buf[i][0] - wantL
				dR := buf[i][1] - wantR
				sigL += wantL * wantL
				errL += dL * dL
				sigR += wantR * wantR
				errR += dR * dR
			}
			snrL := 10 * math.Log10(sigL/errL)
			snrR := 10 * math.Log10(sigR/errR)
			if snrL < tt.minSNRdB {
				t.Errorf("left SNR = %.2f dB, want >= %.2f dB", snrL, tt.minSNRdB)
			}
			if snrR < tt.minSNRdB {
				t.Errorf("right SNR = %.2f dB, want >= %.2f dB", snrR, tt.minSNRdB)
			}
		})
	}
}

func TestResampleDownsampleAntiAliasing(t *testing.T) {
	// A 36 kHz tone at 96 kHz lies above the 24 kHz Nyquist of 48 kHz output.
	src := &stereoSineStreamer{freqL: 36000, freqR: 36000, sr: 96000, count: -1}
	r := resampleWithHeadroom(4, 96000, 48000, src)
	buf := make([][2]float64, 4096)
	n, ok := r.Stream(buf)
	if n != len(buf) || !ok {
		t.Fatalf("Stream() = (%d, %v), want (%d, true)", n, ok, len(buf))
	}
	var peak float64
	for i := 256; i < n-256; i++ {
		peak = max(peak, math.Abs(buf[i][0]))
	}
	attenuationDB := -20 * math.Log10(peak/resampleHeadroomGain)
	if attenuationDB < 70 {
		t.Fatalf("36 kHz stopband attenuation = %.2f dB (peak=%e), want >= 70 dB", attenuationDB, peak)
	}
}

func TestResampleEOFDrainAndExactCount(t *testing.T) {
	tests := []struct {
		name      string
		from      beep.SampleRate
		to        beep.SampleRate
		inFrames  int
		chunkSize int
		wantOut   int
	}{
		{name: "empty source", from: 44100, to: 48000, inFrames: 0, chunkSize: 128, wantOut: 0},
		{name: "single frame 2x up", from: 22050, to: 44100, inFrames: 1, chunkSize: 16, wantOut: 2},
		{name: "44.1k to 48k 1000 frames", from: 44100, to: 48000, inFrames: 1000, chunkSize: 257, wantOut: 1089},
		{name: "96k to 48k 1000 frames", from: 96000, to: 48000, inFrames: 1000, chunkSize: 128, wantOut: 500},
		{name: "96k to 48k 1001 frames", from: 96000, to: 48000, inFrames: 1001, chunkSize: 128, wantOut: 501},
		{name: "22.05k to 48k interpolated", from: 22050, to: 48000, inFrames: 147, chunkSize: 64, wantOut: 320},
		{name: "large request past buffer cap", from: 44100, to: 48000, inFrames: 10000, chunkSize: 12000, wantOut: 10885},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			src := &fakeStreamer{val: [2]float64{0.5, -0.5}, count: tt.inFrames}
			r := resampleWithHeadroom(4, tt.from, tt.to, src)
			buf := make([][2]float64, tt.chunkSize)
			total := 0
			for {
				n, ok := r.Stream(buf)
				total += n
				if !ok {
					if n != 0 {
						t.Fatalf("Stream() returned (%d, false), want (0, false)", n)
					}
					break
				}
				if total > tt.wantOut+1000 {
					t.Fatalf("Stream() did not terminate after %d frames", total)
				}
			}
			if total != tt.wantOut {
				t.Fatalf("total output frames = %d, want %d", total, tt.wantOut)
			}
			// Subsequent calls after EOF must stay drained.
			if n, ok := r.Stream(buf); n != 0 || ok {
				t.Fatalf("post-EOF Stream() = (%d, %v), want (0, false)", n, ok)
			}
		})
	}

	wantErr := errors.New("decode failure")
	r := resampleWithHeadroom(4, 44100, 48000, &errStreamer{err: wantErr})
	if err := r.Err(); !errors.Is(err, wantErr) {
		t.Fatalf("Err() = %v, want %v", err, wantErr)
	}
}

func TestResampleZeroStreamAllocs(t *testing.T) {
	tests := []struct {
		name string
		from beep.SampleRate
		to   beep.SampleRate
	}{
		{name: "exact 44.1k to 48k", from: 44100, to: 48000},
		{name: "exact 96k to 48k", from: 96000, to: 48000},
		{name: "interpolated 22.05k to 48k", from: 22050, to: 48000},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			src := &levelDecoder{level: 0.5, left: -1}
			r := resampleWithHeadroom(4, tt.from, tt.to, src)
			buf := make([][2]float64, 1024)
			allocs := testing.AllocsPerRun(50, func() {
				if n, ok := r.Stream(buf); n != len(buf) || !ok {
					t.Fatalf("Stream() = (%d, %v), want (%d, true)", n, ok, len(buf))
				}
			})
			if allocs != 0 {
				t.Fatalf("Stream() allocations = %v, want 0", allocs)
			}
		})
	}
}

func BenchmarkResample(b *testing.B) {
	cases := []struct {
		name string
		from beep.SampleRate
		to   beep.SampleRate
	}{
		{name: "44.1k_to_48k", from: 44100, to: 48000},
		{name: "96k_to_48k", from: 96000, to: 48000},
		{name: "22.05k_to_48k", from: 22050, to: 48000},
	}
	for _, tc := range cases {
		b.Run("polyphase/"+tc.name, func(b *testing.B) {
			src := &levelDecoder{level: 0.5, left: -1}
			r := resampleWithHeadroom(4, tc.from, tc.to, src)
			buf := make([][2]float64, 1024)
			b.ReportAllocs()
			for b.Loop() {
				r.Stream(buf)
			}
		})
		b.Run("beep/"+tc.name, func(b *testing.B) {
			src := &levelDecoder{level: 0.5, left: -1}
			r := beep.Resample(4, tc.from, tc.to, src)
			buf := make([][2]float64, 1024)
			b.ReportAllocs()
			for b.Loop() {
				r.Stream(buf)
			}
		})
	}
}
