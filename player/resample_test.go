package player

import (
	"math"
	"testing"

	"github.com/gopxl/beep/v2"
)

// hotBroadcastStreamer synthesizes a signal representative of a
// loudness-maximized Icecast/Shoutcast radio stream: several harmonically
// related tones within the source Nyquist range, brickwall-normalized to
// peak at exactly 1.0 (as a limiter on the broadcast side would leave it).
// This reproduces the ~10% post-resample overshoot measured against a real
// RTHK stream (stm2.rthk.hk/radio2, 22050Hz) without shipping copyrighted
// broadcast audio as a test fixture.
type hotBroadcastStreamer struct {
	sr  float64
	pos int
}

func (s *hotBroadcastStreamer) sample(n int) float64 {
	t := float64(n) / s.sr
	v := 0.0
	for _, hz := range []float64{440, 1200, 2500, 4200} {
		v += math.Sin(2 * math.Pi * hz * t)
	}
	return v / 3.2 // brickwall-limited: peaks reach ~1.0
}

func (s *hotBroadcastStreamer) Stream(samples [][2]float64) (int, bool) {
	for i := range samples {
		v := s.sample(s.pos)
		samples[i] = [2]float64{v, v}
		s.pos++
	}
	return len(samples), true
}

func (s *hotBroadcastStreamer) Err() error { return nil }

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
// loudness-maximized signal with beep.Resample's windowed-sinc filter
// overshoots past [-1, 1] (Gibbs-phenomenon ringing), which the final PCM
// output stage then hard-clips even though no source sample exceeded
// full scale.
func TestResampleOvershootClips(t *testing.T) {
	raw := beep.Resample(4, 22050, 44100, &hotBroadcastStreamer{sr: 22050})
	if peak := peakAbs(raw, 2048); peak <= 1.0 {
		t.Fatalf("expected plain beep.Resample to overshoot 1.0 on a hot broadcast-like signal, got peak=%v", peak)
	}
}

// TestResampleWithHeadroomAvoidsClipping verifies the fix: applying
// resampleHeadroomGain before resampling keeps the worst-case overshoot
// within [-1, 1], so the final output stage no longer needs to clip.
func TestResampleWithHeadroomAvoidsClipping(t *testing.T) {
	s := resampleWithHeadroom(4, 22050, 44100, &hotBroadcastStreamer{sr: 22050})
	if peak := peakAbs(s, 2048); peak > 1.0 {
		t.Fatalf("resampleWithHeadroom still overshoots 1.0: peak=%v", peak)
	}
}

// TestResampleWithHeadroomNoopWhenRatesMatch ensures no gain is applied
// (and no beep.Resample wrapping happens) when source and target sample
// rates already match, so non-resampled playback loses no level.
func TestResampleWithHeadroomNoopWhenRatesMatch(t *testing.T) {
	src := &hotBroadcastStreamer{sr: 44100}
	unwrapped := &hotBroadcastStreamer{sr: 44100}
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
