package player

import (
	"math"
	"sync/atomic"

	"github.com/gopxl/beep/v2"
)

// eqFreqs are the center frequencies for the 10-band parametric equalizer.
var eqFreqs = [10]float64{70, 180, 320, 600, 1000, 3000, 6000, 12000, 14000, 16000}

// eqFlatEpsilon is the gain window a band's filter is skipped within (see
// eqBypassed). bitperfect.go reuses it to decide "EQ flat" so the indicator
// can never disagree with what the filter actually does.
const eqFlatEpsilon = 0.1

// eqBypassed reports whether a band's gain is small enough that its filter is
// skipped entirely.
func eqBypassed(dB float64) bool {
	return dB > -eqFlatEpsilon && dB < eqFlatEpsilon
}

// biquad implements a second-order IIR peaking equalizer per the Audio EQ Cookbook.
// Each filter reads its gain from a shared pointer, so EQ changes take
// effect on the next Stream() call without rebuilding the pipeline.
type biquad struct {
	s    beep.Streamer
	freq float64
	q    float64
	gain *atomic.Uint64 // points to Player.eqBands[i], stores Float64bits
	// sr points to Player.rate. In bit-perfect mode the output rate follows
	// each track, so the coefficients are recomputed whenever it changes.
	sr *atomic.Int64
	// Per-channel filter state
	x1, x2 [2]float64
	y1, y2 [2]float64
	// Cached coefficients
	lastGain           float64
	lastSR             int64
	b0, b1, b2, a1, a2 float64
	inited             bool
}

func newBiquad(s beep.Streamer, freq, q float64, gain *atomic.Uint64, sr *atomic.Int64) *biquad {
	return &biquad{s: s, freq: freq, q: q, gain: gain, sr: sr}
}

func (b *biquad) calcCoeffs(dB float64, sr int64) {
	if b.inited && dB == b.lastGain && sr == b.lastSR {
		return
	}
	b.lastGain = dB
	b.lastSR = sr
	b.inited = true

	a := math.Pow(10, dB/40)
	w0 := 2 * math.Pi * b.freq / float64(sr)
	sinW0 := math.Sin(w0)
	cosW0 := math.Cos(w0)
	alpha := sinW0 / (2 * b.q)

	b0 := 1 + alpha*a
	b1 := -2 * cosW0
	b2 := 1 - alpha*a
	a0 := 1 + alpha/a
	a1 := -2 * cosW0
	a2 := 1 - alpha/a

	b.b0 = b0 / a0
	b.b1 = b1 / a0
	b.b2 = b2 / a0
	b.a1 = a1 / a0
	b.a2 = a2 / a0
}

func (b *biquad) Stream(samples [][2]float64) (int, bool) {
	n, ok := b.s.Stream(samples)
	dB := math.Float64frombits(b.gain.Load())

	// Skip processing when gain is effectively zero. eqBypassed is shared with
	// the bit-perfect check so the indicator can never claim a filter is
	// bypassed while it is actually running (or the reverse).
	if eqBypassed(dB) {
		return n, ok
	}

	sr := b.sr.Load()
	if sr <= 0 {
		return n, ok
	}
	b.calcCoeffs(dB, sr)

	for i := range n {
		for ch := range 2 {
			x := samples[i][ch]
			y := b.b0*x + b.b1*b.x1[ch] + b.b2*b.x2[ch] - b.a1*b.y1[ch] - b.a2*b.y2[ch]
			b.x2[ch] = b.x1[ch]
			b.x1[ch] = x
			b.y2[ch] = b.y1[ch]
			b.y1[ch] = y
			samples[i][ch] = y
		}
	}
	return n, ok
}

func (b *biquad) Err() error { return b.s.Err() }
