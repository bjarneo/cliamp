package player

import (
	"math"

	"github.com/gopxl/beep/v2"
)

// resampleHeadroomGain is the linear gain folded into the resampler filter
// coefficients. Windowed-sinc interpolation can overshoot a full-scale signal
// such as a loudness-normalized radio stream. A -2 dB gain in the taps keeps
// intersample peaks below 1.0 before the int16 output stage.
const resampleHeadroomGain = 0.7943282347242815 // 10^(-2/20)

const (
	resampleMaxExactPhases = 256
	resampleInterpPhases   = 256
	resampleMaxHalfLen     = 64
	resampleMinReadFrames  = 512
	resampleBufFrames      = 4096
)

// resampleWithHeadroom resamples s from from to to with resampleHeadroomGain
// folded into the filter taps. When the rates match, it returns s unchanged.
func resampleWithHeadroom(quality int, from, to beep.SampleRate, s beep.Streamer) beep.Streamer {
	if from == to || from <= 0 || to <= 0 {
		return s
	}
	return newPolyphaseResampler(quality, from, to, s)
}

// polyphaseResampler converts a stereo stream between sample rates with a
// Kaiser-windowed sinc FIR filter. It shares each phase weight across both
// channels and folds resampleHeadroomGain into every phase row.
type polyphaseResampler struct {
	s       beep.Streamer
	num     int64
	den     int64
	stepInt int64
	stepRem int64
	invDen  float64
	halfLen int
	taps    int

	interp    bool
	table     []float64
	interpTbl [][2]float64

	buf       [][2]float64
	bufStart  int64
	bufLen    int
	totalIn   int64
	srcIdx    int64
	rem       int64
	eof       bool
	paddedEnd bool
}

func newPolyphaseResampler(quality int, from, to beep.SampleRate, s beep.Streamer) *polyphaseResampler {
	q := min(max(quality, 1), 8)
	baseHalfLen := q * 4
	beta := 5.5 + 1.0*float64(q-1)

	// Pull the cutoff below Nyquist by a fraction of the Kaiser transition width
	// so imaging and aliasing stop at Nyquist and Gibbs overshoot stays bounded.
	ratio := 1.0
	halfLen := baseHalfLen
	if to < from {
		ratio = float64(to) / float64(from)
		halfLen = int(math.Ceil(float64(baseHalfLen) / ratio))
		if halfLen > resampleMaxHalfLen {
			halfLen = resampleMaxHalfLen
		}
		if halfLen%2 != 0 {
			halfLen++
		}
	}
	taps := 2 * halfLen
	fc := 0.88 * ratio

	g := gcdInt(int(from), int(to))
	num := int64(int(from) / g)
	den := int64(int(to) / g)

	r := &polyphaseResampler{
		s:       s,
		num:     num,
		den:     den,
		stepInt: num / den,
		stepRem: num % den,
		invDen:  1.0 / float64(den),
		halfLen: halfLen,
		taps:    taps,
	}

	invI0Beta := 1.0 / besselI0(beta)
	if den <= resampleMaxExactPhases {
		r.table = make([]float64, int(den)*taps)
		for p := range int(den) {
			frac := float64(p) * r.invDen
			fillSincPhaseRow(r.table[p*taps:(p+1)*taps], frac, halfLen, fc, beta, invI0Beta)
		}
	} else {
		r.interp = true
		raw := make([]float64, (resampleInterpPhases+1)*taps)
		invPhases := 1.0 / float64(resampleInterpPhases)
		for p := range resampleInterpPhases + 1 {
			frac := float64(p) * invPhases
			fillSincPhaseRow(raw[p*taps:(p+1)*taps], frac, halfLen, fc, beta, invI0Beta)
		}
		r.interpTbl = make([][2]float64, resampleInterpPhases*taps)
		for p := range resampleInterpPhases {
			base := p * taps
			next := (p + 1) * taps
			for k := range taps {
				w0 := raw[base+k]
				w1 := raw[next+k]
				r.interpTbl[base+k] = [2]float64{w0, w1 - w0}
			}
		}
	}

	bufCap := max(resampleBufFrames, 4*taps) + 2*taps
	pad := halfLen - 1
	r.buf = make([][2]float64, bufCap)
	r.bufStart = -int64(pad)
	r.bufLen = pad
	return r
}

func fillSincPhaseRow(row []float64, frac float64, halfLen int, fc, beta, invI0Beta float64) {
	taps := 2 * halfLen
	invHalf := 1.0 / float64(halfLen)
	var sum float64
	for k := range taps {
		dist := float64(halfLen-1-k) + frac
		u := dist * invHalf
		var win float64
		if u >= -1 && u <= 1 {
			sq := 1 - u*u
			if sq < 0 {
				sq = 0
			}
			win = besselI0(beta*math.Sqrt(sq)) * invI0Beta
		}
		w := sinc(fc*dist) * win
		row[k] = w
		sum += w
	}
	scale := resampleHeadroomGain / sum
	for k := range taps {
		row[k] *= scale
	}
}

func gcdInt(a, b int) int {
	for b != 0 {
		a, b = b, a%b
	}
	if a <= 0 {
		return 1
	}
	return a
}

// besselI0 returns the zeroth-order modified Bessel function of the first kind.
func besselI0(x float64) float64 {
	sum := 1.0
	term := 1.0
	halfX := x * 0.5
	halfX2 := halfX * halfX
	for k := 1; k <= 40; k++ {
		fk := float64(k)
		term *= halfX2 / (fk * fk)
		sum += term
		if term <= sum*1e-16 {
			break
		}
	}
	return sum
}

func sinc(x float64) float64 {
	if x == 0 {
		return 1
	}
	px := math.Pi * x
	return math.Sin(px) / px
}

func (r *polyphaseResampler) refill(remOut int) {
	keepFrom := r.srcIdx - int64(r.halfLen-1)
	if drop := int(keepFrom - r.bufStart); drop > 0 {
		keep := r.bufLen - drop
		if keep > 0 {
			copy(r.buf[:keep], r.buf[drop:r.bufLen])
		} else {
			keep = 0
		}
		r.bufStart += int64(drop)
		r.bufLen = keep
	}

	maxBufLen := len(r.buf) - r.halfLen
	wantLen := maxBufLen
	var targetEnd int64
	if remOut <= maxBufLen {
		lastSrcIdx := r.srcIdx + ((int64(remOut-1)*r.num + r.rem) / r.den)
		targetEnd = lastSrcIdx + int64(r.halfLen) + 1
		if cand := int(targetEnd - r.bufStart); cand < wantLen {
			wantLen = cand
		}
	}
	if wantLen < r.bufLen+resampleMinReadFrames {
		wantLen = min(maxBufLen, r.bufLen+resampleMinReadFrames)
	}

	for !r.eof && r.bufLen < wantLen {
		sn, ok := r.s.Stream(r.buf[r.bufLen:wantLen])
		r.bufLen += sn
		r.totalIn += int64(sn)
		if !ok || sn == 0 {
			r.eof = true
			break
		}
		if targetEnd > 0 && r.bufStart+int64(r.bufLen) >= targetEnd {
			break
		}
	}
	if r.eof && !r.paddedEnd {
		clear(r.buf[r.bufLen : r.bufLen+r.halfLen])
		r.bufLen += r.halfLen
		r.paddedEnd = true
	}
}

// Reset discards buffered input and filter history so a resampler can resume
// cleanly after its underlying source seeks.
func (r *polyphaseResampler) Reset() {
	pad := r.halfLen - 1
	r.bufStart = -int64(pad)
	r.bufLen = pad
	clear(r.buf[:pad])
	r.totalIn = 0
	r.srcIdx = 0
	r.rem = 0
	r.eof = false
	r.paddedEnd = false
}

func (r *polyphaseResampler) Stream(samples [][2]float64) (int, bool) {
	if len(samples) == 0 {
		return 0, !(r.eof && r.srcIdx >= r.totalIn)
	}

	written := 0
	taps := r.taps
	halfLen := r.halfLen
	pad := int64(halfLen - 1)
	stepInt := r.stepInt
	stepRem := r.stepRem
	den := r.den

	for written < len(samples) {
		if r.eof && r.srcIdx >= r.totalIn {
			break
		}
		needMaxIdx := r.srcIdx + int64(halfLen)
		if !r.paddedEnd && r.bufStart+int64(r.bufLen) <= needMaxIdx {
			r.refill(len(samples) - written)
			if r.eof && r.srcIdx >= r.totalIn {
				break
			}
		}

		maxIdx := r.bufStart + int64(r.bufLen) - 1 - int64(halfLen)
		if r.eof && r.totalIn-1 < maxIdx {
			maxIdx = r.totalIn - 1
		}
		if r.srcIdx > maxIdx {
			break
		}

		srcIdx := r.srcIdx
		rem := r.rem
		bufStart := r.bufStart
		buf := r.buf

		if !r.interp {
			table := r.table
			for written < len(samples) && srcIdx <= maxIdx {
				winStart := int(srcIdx - pad - bufStart)
				win := buf[winStart : winStart+taps]
				off := int(rem) * taps
				coeff := table[off : off+taps]
				var sumL0, sumL1, sumR0, sumR1 float64
				for len(coeff) >= 4 && len(win) >= 4 {
					c0, c1, c2, c3 := coeff[0], coeff[1], coeff[2], coeff[3]
					s0, s1, s2, s3 := win[0], win[1], win[2], win[3]
					sumL0 += s0[0]*c0 + s1[0]*c1
					sumL1 += s2[0]*c2 + s3[0]*c3
					sumR0 += s0[1]*c0 + s1[1]*c1
					sumR1 += s2[1]*c2 + s3[1]*c3
					coeff = coeff[4:]
					win = win[4:]
				}
				samples[written] = [2]float64{sumL0 + sumL1, sumR0 + sumR1}
				written++
				srcIdx += stepInt
				rem += stepRem
				if rem >= den {
					rem -= den
					srcIdx++
				}
			}
		} else {
			interpTbl := r.interpTbl
			scale := r.invDen * float64(resampleInterpPhases)
			for written < len(samples) && srcIdx <= maxIdx {
				winStart := int(srcIdx - pad - bufStart)
				win := buf[winStart : winStart+taps]
				pf := float64(rem) * scale
				pi := int(pf)
				if pi >= resampleInterpPhases {
					pi = resampleInterpPhases - 1
				}
				mu := pf - float64(pi)
				off := pi * taps
				coeff := interpTbl[off : off+taps]
				var sumL0, sumL1, sumR0, sumR1 float64
				for len(coeff) >= 4 && len(win) >= 4 {
					p0, p1, p2, p3 := coeff[0], coeff[1], coeff[2], coeff[3]
					c0 := p0[0] + mu*p0[1]
					c1 := p1[0] + mu*p1[1]
					c2 := p2[0] + mu*p2[1]
					c3 := p3[0] + mu*p3[1]
					s0, s1, s2, s3 := win[0], win[1], win[2], win[3]
					sumL0 += s0[0]*c0 + s1[0]*c1
					sumL1 += s2[0]*c2 + s3[0]*c3
					sumR0 += s0[1]*c0 + s1[1]*c1
					sumR1 += s2[1]*c2 + s3[1]*c3
					coeff = coeff[4:]
					win = win[4:]
				}
				samples[written] = [2]float64{sumL0 + sumL1, sumR0 + sumR1}
				written++
				srcIdx += stepInt
				rem += stepRem
				if rem >= den {
					rem -= den
					srcIdx++
				}
			}
		}

		r.srcIdx = srcIdx
		r.rem = rem
	}

	return written, written > 0
}

func (r *polyphaseResampler) Err() error { return r.s.Err() }
