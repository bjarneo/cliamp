package player

import (
	"math"
	"math/bits"
	"sync/atomic"

	"github.com/gopxl/beep/v2"
)

// eqFreqs are the center frequencies for the 10-band parametric equalizer.
var eqFreqs = [10]float64{70, 180, 320, 600, 1000, 3000, 6000, 12000, 14000, 16000}

const (
	eqBandCount = 10
	eqBlockSize = 256
	eqDefaultQ  = 1.4
	eqBandMask  = (1 << eqBandCount) - 1
)

func isNonFinite(v float64) bool {
	return (math.Float64bits(v)>>52)&0x7ff == 0x7ff
}

func isEQGainActive(dB float64) bool {
	return !isNonFinite(dB) && (dB <= -0.1 || dB >= 0.1)
}

// biquadBand holds the frequency constants, coefficients, and stereo history
// for one second-order peaking filter.
type biquadBand struct {
	freq float64
	q    float64
	sr   float64

	// Immutable frequency constants computed at construction.
	valid     bool
	w0        float64
	sinW0     float64
	cosW0     float64
	neg2CosW0 float64
	alpha     float64

	// Per-channel filter state.
	x1, x2 [2]float64
	y1, y2 [2]float64

	// Cached coefficients.
	lastGain           float64
	b0, b1, b2, a1, a2 float64
	inited             bool
	wasActive          bool
}

func initBiquadBand(freq, q, sr float64) biquadBand {
	b := biquadBand{freq: freq, q: q, sr: sr}
	if sr > 0 && freq > 0 && freq < sr*0.495 && q > 0 {
		w0 := 2 * math.Pi * freq / sr
		sinW0 := math.Sin(w0)
		cosW0 := math.Cos(w0)
		b.valid = true
		b.w0 = w0
		b.sinW0 = sinW0
		b.cosW0 = cosW0
		b.neg2CosW0 = -2 * cosW0
		b.alpha = sinW0 / (2 * q)
	}
	return b
}

func (b *biquadBand) resetState() {
	b.x1 = [2]float64{}
	b.x2 = [2]float64{}
	b.y1 = [2]float64{}
	b.y2 = [2]float64{}
}

func (b *biquadBand) bypassed(dB float64) bool {
	return !b.valid || (dB > -0.1 && dB < 0.1) || isNonFinite(dB)
}

func (b *biquadBand) calcCoeffs(dB float64) {
	if b.bypassed(dB) {
		b.resetState()
		b.b0, b.b1, b.b2, b.a1, b.a2 = 1, 0, 0, 0, 0
		if !math.IsNaN(dB) {
			b.lastGain = dB
			b.inited = true
		}
		return
	}
	if b.inited && dB == b.lastGain {
		return
	}
	b.lastGain = dB
	b.inited = true

	a := math.Pow(10, dB/40)
	alpha := b.alpha
	invA0 := 1 / (1 + alpha/a)

	b.b0 = (1 + alpha*a) * invA0
	b.b1 = b.neg2CosW0 * invA0
	b.b2 = (1 - alpha*a) * invA0
	b.a1 = b.b1
	b.a2 = (1 - alpha/a) * invA0
}

func (b *biquadBand) processBlock(block [][2]float64) {
	b0, b1, b2, a1, a2 := b.b0, b.b1, b.b2, b.a1, b.a2
	x1L, x2L, y1L, y2L := b.x1[0], b.x2[0], b.y1[0], b.y2[0]
	x1R, x2R, y1R, y2R := b.x1[1], b.x2[1], b.y1[1], b.y2[1]

	for i := range block {
		xL := block[i][0]
		yL := (b0*xL + b1*x1L + b2*x2L - a2*y2L) - a1*y1L
		if isNonFinite(yL) {
			x1L, x2L, y1L, y2L = 0, 0, 0, 0
			yL = 0
		} else {
			if math.Abs(yL) < 1e-30 {
				yL = 0
			}
			x2L, x1L = x1L, xL
			y2L, y1L = y1L, yL
		}

		xR := block[i][1]
		yR := (b0*xR + b1*x1R + b2*x2R - a2*y2R) - a1*y1R
		if isNonFinite(yR) {
			x1R, x2R, y1R, y2R = 0, 0, 0, 0
			yR = 0
		} else {
			if math.Abs(yR) < 1e-30 {
				yR = 0
			}
			x2R, x1R = x1R, xR
			y2R, y1R = y1R, yR
		}

		block[i][0] = yL
		block[i][1] = yR
	}

	b.x1[0], b.x2[0], b.y1[0], b.y2[0] = x1L, x2L, y1L, y2L
	b.x1[1], b.x2[1], b.y1[1], b.y2[1] = x1R, x2R, y1R, y2R
}

// eqStreamer applies all 10 peaking EQ bands in one streamer stage.
// It returns immediately when all bands are flat and have no active history.
type eqStreamer struct {
	s          beep.Streamer
	gains      *[eqBandCount]atomic.Uint64
	activeMask *atomic.Uint32
	bands      [eqBandCount]biquadBand
	prevActive uint32
}

func newEQStreamer(s beep.Streamer, gains *[eqBandCount]atomic.Uint64, activeMask *atomic.Uint32, sr float64) *eqStreamer {
	e := &eqStreamer{
		s:          s,
		gains:      gains,
		activeMask: activeMask,
	}
	for i := range eqBandCount {
		e.bands[i] = initBiquadBand(eqFreqs[i], eqDefaultQ, sr)
	}
	return e
}

func (e *eqStreamer) Stream(samples [][2]float64) (int, bool) {
	n, ok := e.s.Stream(samples)
	if n == 0 {
		return 0, ok
	}

	var checkMask uint32
	if e.activeMask != nil {
		checkMask = (e.activeMask.Load() | e.prevActive) & eqBandMask
		if checkMask == 0 {
			return n, ok
		}
	} else {
		if e.prevActive == 0 {
			var bitsOr uint64
			for i := range eqBandCount {
				bitsOr |= e.gains[i].Load()
			}
			if bitsOr == 0 {
				return n, ok
			}
		}
		checkMask = eqBandMask
	}

	var active [eqBandCount]int
	activeCount := 0
	var newActive uint32
	for mask := checkMask; mask != 0; mask &= mask - 1 {
		i := bits.TrailingZeros32(mask)
		b := &e.bands[i]
		dB := math.Float64frombits(e.gains[i].Load())
		if b.bypassed(dB) {
			if b.wasActive {
				b.resetState()
				b.wasActive = false
			}
			continue
		}
		b.calcCoeffs(dB)
		b.wasActive = true
		newActive |= 1 << i
		active[activeCount] = i
		activeCount++
	}
	e.prevActive = newActive
	if activeCount == 0 {
		return n, ok
	}

	buf := samples[:n]
	for start := 0; start < n; start += eqBlockSize {
		end := min(start+eqBlockSize, n)
		block := buf[start:end]
		for _, idx := range active[:activeCount] {
			e.bands[idx].processBlock(block)
		}
	}
	return n, ok
}

func (e *eqStreamer) Err() error { return e.s.Err() }

// biquad wraps one biquadBand as a standalone beep.Streamer.
type biquad struct {
	biquadBand
	s    beep.Streamer
	gain *atomic.Uint64
}

func newBiquad(s beep.Streamer, freq, q float64, gain *atomic.Uint64, sr float64) *biquad {
	return &biquad{
		biquadBand: initBiquadBand(freq, q, sr),
		s:          s,
		gain:       gain,
	}
}

func (b *biquad) Stream(samples [][2]float64) (int, bool) {
	n, ok := b.s.Stream(samples)
	dB := math.Float64frombits(b.gain.Load())

	if b.bypassed(dB) {
		b.resetState()
		b.wasActive = false
		return n, ok
	}

	b.calcCoeffs(dB)
	b.wasActive = true
	if n > 0 {
		b.processBlock(samples[:n])
	}
	return n, ok
}

func (b *biquad) Err() error { return b.s.Err() }
