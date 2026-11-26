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

func TestBiquadBypassClearsState(t *testing.T) {
	tests := []struct {
		name string
		freq float64
		sr   float64
		dB   float64
	}{
		{name: "near zero positive gain", freq: 1000, sr: 44100, dB: 0.05},
		{name: "zero gain", freq: 1000, sr: 44100, dB: 0},
		{name: "near zero negative gain", freq: 1000, sr: 44100, dB: -0.09},
		{name: "at Nyquist threshold", freq: 44100 * 0.495, sr: 44100, dB: 6},
		{name: "above Nyquist", freq: 24000, sr: 44100, dB: 12},
		{name: "NaN gain", freq: 1000, sr: 44100, dB: math.NaN()},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var gain atomic.Uint64
			gain.Store(math.Float64bits(tt.dB))
			b := newBiquad(&fakeStreamer{val: [2]float64{0.4, -0.2}, count: 4}, tt.freq, 1.4, &gain, tt.sr)
			b.x1 = [2]float64{1, 1}
			b.x2 = [2]float64{2, 2}
			b.y1 = [2]float64{3, 3}
			b.y2 = [2]float64{4, 4}

			b.calcCoeffs(tt.dB)
			if b.b0 != 1 || b.b1 != 0 || b.b2 != 0 || b.a1 != 0 || b.a2 != 0 {
				t.Fatalf("calcCoeffs bypass coeffs = (%v, %v, %v, %v, %v), want unity", b.b0, b.b1, b.b2, b.a1, b.a2)
			}
			if b.x1 != ([2]float64{}) || b.x2 != ([2]float64{}) || b.y1 != ([2]float64{}) || b.y2 != ([2]float64{}) {
				t.Fatalf("calcCoeffs did not clear state: x1=%v x2=%v y1=%v y2=%v", b.x1, b.x2, b.y1, b.y2)
			}

			b.x1 = [2]float64{0.5, -0.5}
			b.y1 = [2]float64{0.5, -0.5}
			out := make([][2]float64, 4)
			n, ok := b.Stream(out)
			if n != 4 || !ok {
				t.Fatalf("Stream() = (%d, %v), want (4, true)", n, ok)
			}
			if b.x1 != ([2]float64{}) || b.x2 != ([2]float64{}) || b.y1 != ([2]float64{}) || b.y2 != ([2]float64{}) {
				t.Fatalf("Stream bypass did not clear state: x1=%v x2=%v y1=%v y2=%v", b.x1, b.x2, b.y1, b.y2)
			}
			for i := range n {
				if out[i] != ([2]float64{0.4, -0.2}) {
					t.Fatalf("out[%d] = %v, want passthrough [0.4 -0.2]", i, out[i])
				}
			}
		})
	}
}

func TestBiquadGuardsNonFiniteAndFlushesSubnormal(t *testing.T) {
	tests := []struct {
		name    string
		in      [][2]float64
		initY1  [2]float64
		wantOut float64
		wantY1  float64
	}{
		{
			name:    "NaN input resets state",
			in:      [][2]float64{{math.NaN(), 0.25}, {0.5, 0.25}},
			wantOut: 0,
		},
		{
			name:    "positive Inf input resets state",
			in:      [][2]float64{{math.Inf(1), 0.25}, {0.5, 0.25}},
			wantOut: 0,
		},
		{
			name:    "negative Inf input resets state",
			in:      [][2]float64{{math.Inf(-1), 0.25}, {0.5, 0.25}},
			wantOut: 0,
		},
		{
			name:    "non-finite output from poisoned state resets state",
			in:      [][2]float64{{0.1, 0.1}, {0.5, 0.5}},
			initY1:  [2]float64{math.Inf(1), 0},
			wantOut: 0,
		},
		{
			name:    "subnormal output flushes to zero",
			in:      [][2]float64{{1e-35, -1e-35}},
			wantOut: 0,
			wantY1:  0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var gain atomic.Uint64
			gain.Store(math.Float64bits(6.0))
			b := newBiquad(&sequenceStreamer{samples: tt.in}, 1000, 1.4, &gain, 44100)
			b.y1 = tt.initY1

			out := make([][2]float64, len(tt.in))
			n, _ := b.Stream(out)
			if n != len(tt.in) {
				t.Fatalf("Stream() = %d, want %d", n, len(tt.in))
			}
			if out[0][0] != tt.wantOut {
				t.Fatalf("out[0][0] = %v, want %v", out[0][0], tt.wantOut)
			}
			for i := range n {
				for ch := range 2 {
					if math.IsNaN(out[i][ch]) || math.IsInf(out[i][ch], 0) {
						t.Fatalf("out[%d][%d] = %v, want finite", i, ch, out[i][ch])
					}
				}
			}
			if len(tt.in) > 1 && out[1][0] == 0 {
				t.Fatalf("filter did not recover on subsequent finite sample: out[1] = %v", out[1])
			}
			if len(tt.in) == 1 && b.y1[0] != tt.wantY1 {
				t.Fatalf("y1[0] = %v, want %v", b.y1[0], tt.wantY1)
			}
		})
	}
}
