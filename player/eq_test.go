package player

import (
	"math"
	"sync/atomic"
	"testing"
)

func TestBiquadPassthroughAtZeroDB(t *testing.T) {
	src := &fakeStreamer{val: [2]float64{0.7, -0.3}, count: 4}
	var gain atomic.Uint64
	gain.Store(math.Float64bits(0.0))

	b := newBiquad(src, 1000, 0.707, &gain, 44100)

	samples := make([][2]float64, 4)
	n, _ := b.Stream(samples)

	for i := range n {
		if math.Abs(samples[i][0]-0.7) > 1e-9 {
			t.Errorf("samples[%d][0] = %f, want 0.7", i, samples[i][0])
		}
		if math.Abs(samples[i][1]-(-0.3)) > 1e-9 {
			t.Errorf("samples[%d][1] = %f, want -0.3", i, samples[i][1])
		}
	}
}

func TestBiquadNonZeroGainModifiesSamples(t *testing.T) {
	// Sine wave at center frequency — DC input would pass through unchanged.
	const sr = 44100
	const freq = 1000.0
	const nSamples = 512
	src := &sineStreamer{freq: freq, sr: sr, count: nSamples}
	var gain atomic.Uint64
	gain.Store(math.Float64bits(12.0)) // +12 dB boost

	b := newBiquad(src, freq, 0.707, &gain, sr)

	samples := make([][2]float64, nSamples)
	n, _ := b.Stream(samples)

	maxAmp := 0.0
	for i := 256; i < n; i++ {
		if a := math.Abs(samples[i][0]); a > maxAmp {
			maxAmp = a
		}
	}
	if maxAmp <= 1.05 {
		t.Errorf("biquad at +12dB: max amplitude = %f, expected > 1.05", maxAmp)
	}
}

func TestBiquadCoeffCaching(t *testing.T) {
	var gain atomic.Uint64
	gain.Store(math.Float64bits(3.0))

	b := newBiquad(&fakeStreamer{count: 0}, 1000, 0.707, &gain, 44100)

	b.calcCoeffs(3.0)
	b0First := b.b0
	if !b.inited {
		t.Fatal("inited should be true after calcCoeffs")
	}

	b.calcCoeffs(3.0)
	if b.b0 != b0First {
		t.Error("coefficients should be cached for same gain")
	}

	b.calcCoeffs(6.0)
	if b.b0 == b0First {
		t.Error("coefficients should be recomputed for different gain")
	}
	if b.lastGain != 6.0 {
		t.Errorf("lastGain = %f, want 6.0", b.lastGain)
	}
}

func TestBiquadErr(t *testing.T) {
	src := &fakeStreamer{}
	var gain atomic.Uint64

	b := newBiquad(src, 1000, 0.707, &gain, 44100)

	if err := b.Err(); err != nil {
		t.Errorf("Err() = %v, want nil", err)
	}
}

func TestEqFreqs(t *testing.T) {
	for i := 1; i < len(eqFreqs); i++ {
		if eqFreqs[i] <= eqFreqs[i-1] {
			t.Errorf("eqFreqs[%d] (%f) <= eqFreqs[%d] (%f)", i, eqFreqs[i], i-1, eqFreqs[i-1])
		}
	}

	// Should have exactly 10 bands
	if len(eqFreqs) != 10 {
		t.Errorf("len(eqFreqs) = %d, want 10", len(eqFreqs))
	}
}
