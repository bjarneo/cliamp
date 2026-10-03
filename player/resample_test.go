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
