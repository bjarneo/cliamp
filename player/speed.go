package player

import (
	"math"
	"sync/atomic"

	"github.com/gopxl/beep/v2"
)

// Time-stretching constants tuned for speech and music, adapted from
// SoundTouch defaults. The long sequence length keeps crossfade events
// infrequent so most output is a direct source copy.
const (
	tsSeq    = 3584           // sequence length between crossfades
	tsOvlp   = 512            // overlap crossfade region length
	tsWin    = tsSeq + tsOvlp // source window per frame
	tsSearch = 1024           // search radius around expected offset
	tsCoarse = 8              // sample stride for the coarse correlation pass
	tsTop    = 8              // coarse candidates retained for fine refinement

	tsInInitCap     = 16384
	tsOutInitCap    = 8192
	tsMaxSearchSpan = 2*tsSearch + 1
	tsSeenWords     = (tsMaxSearchSpan + 63) / 64
	tsCoarseLen     = tsOvlp / tsCoarse
)

// Pre-computed linear crossfade table where tsAlpha[i] equals i / tsOvlp.
var tsAlpha [tsOvlp]float64

func init() {
	for i := range tsAlpha {
		tsAlpha[i] = float64(i) / float64(tsOvlp)
	}
}

// speedStreamer wraps a beep.Streamer and changes playback speed without
// changing pitch via WSOLA time-stretching. It allocates WSOLA buffers lazily
// on the first non-1.0x call.
type speedStreamer struct {
	s     beep.Streamer
	speed *atomic.Uint64 // ratio as Float64bits, 1.0 is normal speed

	in    [][2]float64 // source buffer, allocated lazily
	inN   int          // valid sample count
	inPos float64      // fractional analysis cursor

	out   [][2]float64 // output buffer, allocated lazily
	outRd int
	outWr int

	inEnergy     []float64 // stride-1 prefix sums of window energy
	coarseEnergy []float64 // stride-tsCoarse prefix sums of window energy

	tail      [tsOvlp][2]float64 // previous frame trailing samples for crossfade
	tailValid bool
}

func newSpeedStreamer(s beep.Streamer, speed *atomic.Uint64) *speedStreamer {
	return &speedStreamer{
		s:     s,
		speed: speed,
	}
}

func (ss *speedStreamer) ensureBuffers() {
	if ss.in == nil {
		ss.in = make([][2]float64, tsInInitCap)
	}
	if ss.out == nil {
		ss.out = make([][2]float64, tsOutInitCap)
	}
}

// Stream produces output samples. At speed 1.0x it passes through directly.
// At other speeds it applies WSOLA time-stretching to preserve pitch.
func (ss *speedStreamer) Stream(samples [][2]float64) (int, bool) {
	speed := math.Float64frombits(ss.speed.Load())
	if (speed == 1.0 || speed <= 0) && ss.outWr == ss.outRd && ss.inN == 0 && !ss.tailValid {
		return ss.s.Stream(samples)
	}
	if speed <= 0 || speed == 1.0 {
		return ss.passthrough(samples)
	}

	ss.ensureBuffers()
	for ss.outWr-ss.outRd < len(samples) {
		if !ss.wsolaFrame(speed) {
			break
		}
	}
	n := ss.drainOut(samples)
	return n, n > 0
}

func (ss *speedStreamer) passthrough(samples [][2]float64) (int, bool) {
	d := ss.drainOut(samples)
	if d == len(samples) {
		return d, true
	}
	// Drain unconsumed source samples before switching to direct reads.
	srcStart := int(math.Round(ss.inPos))
	if srcAvail := ss.inN - srcStart; srcAvail > 0 {
		n := min(len(samples)-d, srcAvail)
		copy(samples[d:d+n], ss.in[srcStart:srcStart+n])
		d += n
		ss.inPos += float64(n)
		if d == len(samples) {
			return d, true
		}
	}
	// Reset WSOLA state for clean re-entry.
	ss.outRd = 0
	ss.outWr = 0
	ss.inN = 0
	ss.inPos = 0
	ss.tailValid = false
	n, ok := ss.s.Stream(samples[d:])
	total := d + n
	return total, ok || total > 0
}

// Reset discards buffered WSOLA state after a track change or seek.
func (ss *speedStreamer) Reset() {
	if ss == nil {
		return
	}
	ss.tail = [tsOvlp][2]float64{}
	ss.tailValid = false
	ss.inN = 0
	ss.inPos = 0
	ss.outRd = 0
	ss.outWr = 0
}

func (ss *speedStreamer) compactOut() {
	rem := ss.outWr - ss.outRd
	if rem > 0 && ss.outRd > 0 {
		copy(ss.out, ss.out[ss.outRd:ss.outWr])
	}
	ss.outRd = 0
	ss.outWr = rem
}

func (ss *speedStreamer) drainOut(dst [][2]float64) int {
	avail := ss.outWr - ss.outRd
	n := min(len(dst), avail)
	if n <= 0 {
		return 0
	}
	copy(dst[:n], ss.out[ss.outRd:ss.outRd+n])
	ss.outRd += n
	if ss.outRd == ss.outWr {
		ss.outRd = 0
		ss.outWr = 0
	} else if ss.outRd >= tsSeq {
		ss.compactOut()
	}
	return n
}

func (ss *speedStreamer) fillSource(need int) bool {
	if drop := int(ss.inPos) - tsSearch; drop > 0 {
		drop = min(drop, ss.inN)
		keep := ss.inN - drop
		if keep > 0 {
			copy(ss.in[:keep], ss.in[drop:ss.inN])
		} else {
			keep = 0
		}
		ss.inN = keep
		ss.inPos -= float64(drop)
		need = max(0, need-drop)
	}
	for ss.inN < need {
		toRead := max(need-ss.inN, 4096)
		if ss.inN+toRead > cap(ss.in) {
			newIn := make([][2]float64, ss.inN+toRead)
			copy(newIn[:ss.inN], ss.in[:ss.inN])
			ss.in = newIn
		}
		n, _ := ss.s.Stream(ss.in[ss.inN : ss.inN+toRead])
		ss.inN += n
		if n == 0 {
			return ss.inN >= need
		}
	}
	return true
}

// wsolaFrame produces one synthesis frame of tsSeq output samples.
func (ss *speedStreamer) wsolaFrame(speed float64) bool {
	ss.ensureBuffers()
	expected := int(math.Round(ss.inPos))
	needed := expected + tsWin + tsSearch + 1
	filled := ss.fillSource(needed)
	expected = int(math.Round(ss.inPos))
	if !filled && expected+tsSeq > ss.inN {
		return false
	}

	first := !ss.tailValid

	srcOff := expected
	if !first {
		srcOff = ss.searchBestOffset(expected)
	}

	if srcOff+tsWin > ss.inN {
		srcOff = max(0, ss.inN-tsWin)
	}
	if srcOff+tsSeq > ss.inN {
		return false
	}

	// Compact consumed output before growing the output slice.
	if ss.outWr+tsSeq > len(ss.out) && ss.outRd > 0 {
		ss.compactOut()
	}
	if ss.outWr+tsSeq > len(ss.out) {
		newOut := make([][2]float64, ss.outWr+tsSeq+4096)
		copy(newOut[:ss.outWr], ss.out[:ss.outWr])
		ss.out = newOut
	}

	if first {
		copy(ss.out[ss.outWr:ss.outWr+tsSeq], ss.in[srcOff:srcOff+tsSeq])
	} else {
		// Crossfade the overlap region using the pre-computed alpha table.
		for i := range tsOvlp {
			a := tsAlpha[i]
			b := 1 - a
			ss.out[ss.outWr+i] = [2]float64{
				b*ss.tail[i][0] + a*ss.in[srcOff+i][0],
				b*ss.tail[i][1] + a*ss.in[srcOff+i][1],
			}
		}
		copy(ss.out[ss.outWr+tsOvlp:ss.outWr+tsSeq],
			ss.in[srcOff+tsOvlp:srcOff+tsSeq])
	}
	ss.outWr += tsSeq

	// Copy available tail samples and zero any missing remainder.
	tailStart := srcOff + tsSeq
	tailEnd := min(srcOff+tsWin, ss.inN)
	copied := 0
	if tailStart < tailEnd {
		copied = copy(ss.tail[:], ss.in[tailStart:tailEnd])
	}
	clear(ss.tail[copied:])
	ss.tailValid = true

	ss.inPos += float64(tsSeq) * speed
	return true
}

// searchBestOffset finds the source position near expected whose start best
// matches the previous tail via normalized cross-correlation. It precomputes
// prefix-sum energies for O(1) window norms and deduplicates refined offsets
// with a stack bitset.
func (ss *speedStreamer) searchBestOffset(expected int) int {
	maxOff := max(0, ss.inN-tsWin)
	lo := min(max(0, expected-tsSearch), maxOff)
	hi := max(min(maxOff, expected+tsSearch), lo)

	bestOff := min(max(expected, lo), hi)

	var tailEnergy float64
	for i := range tsOvlp {
		tailEnergy += ss.tail[i][0]*ss.tail[i][0] + ss.tail[i][1]*ss.tail[i][1]
	}
	if tailEnergy < 1e-9 || lo+tsOvlp > ss.inN {
		return bestOff
	}

	var coarseTail [tsCoarseLen][2]float64
	var coarseTailEnergy float64
	for j := range tsCoarseLen {
		t := ss.tail[j*tsCoarse]
		coarseTail[j] = t
		coarseTailEnergy += t[0]*t[0] + t[1]*t[1]
	}
	if coarseTailEnergy < 1e-9 {
		return bestOff
	}

	span := (hi - lo) + tsOvlp
	if len(ss.inEnergy) < span+1 {
		ss.inEnergy = make([]float64, 2*tsSearch+tsOvlp+1)
	}
	if len(ss.coarseEnergy) < span+tsCoarse {
		ss.coarseEnergy = make([]float64, 2*tsSearch+tsOvlp+tsCoarse)
	}
	energy := ss.inEnergy[:span+1]
	coarseEnergy := ss.coarseEnergy[:span+tsCoarse]
	energy[0] = 0
	for i := range tsCoarse {
		coarseEnergy[i] = 0
	}
	inSlice := ss.in[lo : lo+span]
	var acc float64
	for i, s := range inSlice {
		e := s[0]*s[0] + s[1]*s[1]
		acc += e
		energy[i+1] = acc
		coarseEnergy[i+tsCoarse] = coarseEnergy[i] + e
	}

	bestK := bestOff - lo
	bestScore := ss.fineScore(bestOff, energy[bestK+tsOvlp]-energy[bestK])

	var seen [tsSeenWords]uint64
	seen[bestK>>6] |= uint64(1) << (bestK & 63)

	// Score every offset with a strided subset of the overlap and keep the top
	// candidates for full-resolution refinement. The four-offset block reuses
	// each tail sample and reads four contiguous source frames per tap.
	type candidate struct {
		off   int
		score float64
	}
	var top [tsTop]candidate
	topN := 0
	insertTop := func(off int, score float64) {
		if score <= 0 {
			return
		}
		at := topN
		for i := range topN {
			if score > top[i].score || (score == top[i].score && off < top[i].off) {
				at = i
				break
			}
		}
		if at >= tsTop {
			return
		}
		if topN < tsTop {
			topN++
		}
		copy(top[at+1:topN], top[at:topN-1])
		top[at] = candidate{off: off, score: score}
	}

	off := lo
	for ; off+3 <= hi; off += 4 {
		inWin := (*[tsOvlp][2]float64)(ss.in[off : off+tsOvlp])
		var c0L, c0R, c1L, c1R, c2L, c2R, c3L, c3R float64
		for j := range tsCoarseLen {
			tL := coarseTail[j][0]
			tR := coarseTail[j][1]
			base := j * tsCoarse
			c0L += tL * inWin[base][0]
			c0R += tR * inWin[base][1]
			c1L += tL * inWin[base+1][0]
			c1R += tR * inWin[base+1][1]
			c2L += tL * inWin[base+2][0]
			c2R += tR * inWin[base+2][1]
			c3L += tL * inWin[base+3][0]
			c3R += tR * inWin[base+3][1]
		}
		k := off - lo
		if c0 := c0L + c0R; c0 > 0 {
			if norm := coarseEnergy[k+tsOvlp] - coarseEnergy[k]; norm >= 1e-9 {
				insertTop(off, c0*c0/norm)
			}
		}
		if c1 := c1L + c1R; c1 > 0 {
			if norm := coarseEnergy[k+1+tsOvlp] - coarseEnergy[k+1]; norm >= 1e-9 {
				insertTop(off+1, c1*c1/norm)
			}
		}
		if c2 := c2L + c2R; c2 > 0 {
			if norm := coarseEnergy[k+2+tsOvlp] - coarseEnergy[k+2]; norm >= 1e-9 {
				insertTop(off+2, c2*c2/norm)
			}
		}
		if c3 := c3L + c3R; c3 > 0 {
			if norm := coarseEnergy[k+3+tsOvlp] - coarseEnergy[k+3]; norm >= 1e-9 {
				insertTop(off+3, c3*c3/norm)
			}
		}
	}
	for ; off <= hi; off++ {
		k := off - lo
		norm := coarseEnergy[k+tsOvlp] - coarseEnergy[k]
		if norm < 1e-9 {
			continue
		}
		inWin := (*[tsOvlp][2]float64)(ss.in[off : off+tsOvlp])
		var cL, cR float64
		for j := range tsCoarseLen {
			t := coarseTail[j]
			s := inWin[j*tsCoarse]
			cL += t[0] * s[0]
			cR += t[1] * s[1]
		}
		if corr := cL + cR; corr > 0 {
			insertTop(off, corr*corr/norm)
		}
	}

	for _, cand := range top[:topN] {
		refineLo := max(lo, cand.off-tsCoarse+1)
		refineHi := min(hi, cand.off+tsCoarse-1)
		for off := refineLo; off <= refineHi; off++ {
			k := off - lo
			word := k >> 6
			bit := uint64(1) << (k & 63)
			if seen[word]&bit != 0 {
				continue
			}
			seen[word] |= bit
			score := ss.fineScore(off, energy[k+tsOvlp]-energy[k])
			if score > bestScore || (score == bestScore && score > 0 && off < bestOff) {
				bestOff, bestScore = off, score
			}
		}
	}
	return bestOff
}

func (ss *speedStreamer) fineScore(off int, norm float64) float64 {
	if norm < 1e-9 {
		return 0
	}
	inWin := (*[tsOvlp][2]float64)(ss.in[off : off+tsOvlp])
	tail := &ss.tail
	var c0, c1, c2, c3, c4, c5, c6, c7 float64
	for i := 0; i < tsOvlp; i += 4 {
		c0 += tail[i][0] * inWin[i][0]
		c1 += tail[i][1] * inWin[i][1]
		c2 += tail[i+1][0] * inWin[i+1][0]
		c3 += tail[i+1][1] * inWin[i+1][1]
		c4 += tail[i+2][0] * inWin[i+2][0]
		c5 += tail[i+2][1] * inWin[i+2][1]
		c6 += tail[i+3][0] * inWin[i+3][0]
		c7 += tail[i+3][1] * inWin[i+3][1]
	}
	corr := ((c0 + c1) + (c2 + c3)) + ((c4 + c5) + (c6 + c7))
	if corr <= 0 {
		return 0
	}
	return corr * corr / norm
}

func (ss *speedStreamer) offsetScore(off, stride int) float64 {
	inWin := (*[tsOvlp][2]float64)(ss.in[off : off+tsOvlp])
	tail := &ss.tail
	var corr, norm float64
	for i := 0; i < tsOvlp; i += stride {
		s := inWin[i]
		t := tail[i]
		corr += t[0]*s[0] + t[1]*s[1]
		norm += s[0]*s[0] + s[1]*s[1]
	}
	if norm < 1e-9 || corr <= 0 {
		return 0
	}
	return corr * corr / norm
}

// Err forwards the underlying streamer error.
func (ss *speedStreamer) Err() error {
	return ss.s.Err()
}
