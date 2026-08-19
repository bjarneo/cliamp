package player

import (
	"math"
	"sync/atomic"

	"github.com/gopxl/beep/v2"
)

// Time-stretching constants tuned for natural speech and music, adapted from
// SoundTouch's proven defaults. The long sequence length means crossfade
// events are infrequent (~12/sec), and most output is a direct source copy.
const (
	tsSeq    = 3584           // sequence: ~81ms @44.1kHz — time between crossfades
	tsOvlp   = 512            // overlap: ~12ms — crossfade region
	tsWin    = tsSeq + tsOvlp // source window per frame (4096)
	tsSearch = 1024           // search: ±~23ms — covers multiple pitch periods
	tsCoarse = 8              // sample stride for the low-cost correlation pass
	tsTop    = 8              // coarse candidates retained for full refinement
)

// Pre-computed linear crossfade table: alpha[i] = i / tsOvlp.
// Avoids per-sample division in the hot crossfade loop.
var tsAlpha [tsOvlp]float64

func init() {
	for i := range tsAlpha {
		tsAlpha[i] = float64(i) / float64(tsOvlp)
	}
}

// speedStreamer wraps a beep.Streamer and adjusts playback speed without
// changing pitch, using WSOLA (Waveform Similarity Overlap-Add) time-stretching.
// The speed ratio is stored atomically so the UI thread can change it
// while the audio thread reads it.
type speedStreamer struct {
	s     beep.Streamer
	speed *atomic.Uint64 // ratio as Float64bits; 1.0 = normal

	in    [][2]float64 // source buffer
	inN   int          // valid sample count
	inPos float64      // fractional analysis cursor

	out   [][2]float64 // output ring buffer
	outRd int
	outWr int

	tail      [tsOvlp][2]float64 // previous frame's trailing samples for crossfade
	tailValid bool
}

func newSpeedStreamer(s beep.Streamer, speed *atomic.Uint64) *speedStreamer {
	return &speedStreamer{
		s:     s,
		speed: speed,
		in:    make([][2]float64, 16384),
		out:   make([][2]float64, 8192),
	}
}

// Stream produces output samples. At speed 1.0x it passes through directly.
// At other speeds it applies WSOLA time-stretching to preserve pitch.
func (ss *speedStreamer) Stream(samples [][2]float64) (int, bool) {
	speed := math.Float64frombits(ss.speed.Load())
	if speed <= 0 || speed == 1.0 {
		return ss.passthrough(samples)
	}

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

func (ss *speedStreamer) drainOut(dst [][2]float64) int {
	avail := ss.outWr - ss.outRd
	n := min(len(dst), avail)
	if n <= 0 {
		return 0
	}
	copy(dst[:n], ss.out[ss.outRd:ss.outRd+n])
	ss.outRd += n
	if ss.outRd > 8192 {
		rem := ss.outWr - ss.outRd
		if rem > 0 {
			copy(ss.out, ss.out[ss.outRd:ss.outWr])
		}
		ss.outRd = 0
		ss.outWr = rem
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
//
// Frame layout in source:
//
//	[crossfade tsOvlp][direct copy tsSeq-tsOvlp][tail tsOvlp]
//	|<----------- tsSeq (output) ------------>||<-- saved -->|
//	|<------------------ tsWin (source read) ---------------->|
func (ss *speedStreamer) wsolaFrame(speed float64) bool {
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

	// Grow output buffer if needed.
	if ss.outWr+tsSeq > cap(ss.out) {
		newOut := make([][2]float64, ss.outWr+tsSeq+4096)
		copy(newOut[:ss.outWr], ss.out[:ss.outWr])
		ss.out = newOut
	}

	if first {
		copy(ss.out[ss.outWr:ss.outWr+tsSeq], ss.in[srcOff:srcOff+tsSeq])
	} else {
		// Crossfade the overlap region using pre-computed alpha table.
		for i := range tsOvlp {
			a := tsAlpha[i]
			b := 1 - a
			ss.out[ss.outWr+i] = [2]float64{
				b*ss.tail[i][0] + a*ss.in[srcOff+i][0],
				b*ss.tail[i][1] + a*ss.in[srcOff+i][1],
			}
		}
		// Direct copy the rest — unmodified source samples.
		copy(ss.out[ss.outWr+tsOvlp:ss.outWr+tsSeq],
			ss.in[srcOff+tsOvlp:srcOff+tsSeq])
	}
	ss.outWr += tsSeq

	// Save tail for next frame's crossfade.
	copy(ss.tail[:], ss.in[srcOff+tsSeq:srcOff+tsWin])
	ss.tailValid = true

	ss.inPos += float64(tsSeq) * speed
	return true
}

// searchBestOffset finds the source position near expected whose start best
// matches the previous tail, using normalized cross-correlation.
// Normalizing prevents bias toward loud sections. If no good match is found
// (all correlations negative or silent), falls back to the expected offset.
func (ss *speedStreamer) searchBestOffset(expected int) int {
	maxOff := max(0, ss.inN-tsWin)
	lo := min(max(0, expected-tsSearch), maxOff)
	hi := max(min(maxOff, expected+tsSearch), lo)

	bestOff := min(max(expected, lo), hi)
	bestScore := ss.offsetScore(bestOff, 1)

	// Score every offset using a strided subset of the overlap. Retaining a few
	// candidates avoids assuming correlation is smooth between adjacent offsets,
	// while still doing far less work than a full-resolution exhaustive pass.
	type candidate struct {
		off   int
		score float64
	}
	var top [tsTop]candidate
	topN := 0
	for off := lo; off <= hi; off++ {
		score := ss.offsetScore(off, tsCoarse)
		if score <= 0 {
			continue
		}
		at := topN
		for i := range topN {
			if score > top[i].score || (score == top[i].score && off < top[i].off) {
				at = i
				break
			}
		}
		if at >= tsTop {
			continue
		}
		if topN < tsTop {
			topN++
		}
		copy(top[at+1:topN], top[at:topN-1])
		top[at] = candidate{off: off, score: score}
	}

	for _, candidate := range top[:topN] {
		refineLo := max(lo, candidate.off-tsCoarse+1)
		refineHi := min(hi, candidate.off+tsCoarse-1)
		for off := refineLo; off <= refineHi; off++ {
			score := ss.offsetScore(off, 1)
			if score > bestScore || (score == bestScore && score > 0 && off < bestOff) {
				bestOff, bestScore = off, score
			}
		}
	}
	if topN == 0 {
		for off := lo; off <= hi; off++ {
			score := ss.offsetScore(off, 1)
			if score > bestScore || (score == bestScore && score > 0 && off < bestOff) {
				bestOff, bestScore = off, score
			}
		}
	}
	return bestOff
}

func (ss *speedStreamer) offsetScore(off, stride int) float64 {
	var corr, norm float64
	for i := 0; i < tsOvlp; i += stride {
		corr += ss.tail[i][0]*ss.in[off+i][0] + ss.tail[i][1]*ss.in[off+i][1]
		norm += ss.in[off+i][0]*ss.in[off+i][0] + ss.in[off+i][1]*ss.in[off+i][1]
	}
	if norm < 1e-9 || corr <= 0 {
		return 0
	}
	// corr^2/norm avoids sqrt; equivalent ranking to corr/sqrt(norm).
	return corr * corr / norm
}

// Err forwards to the wrapped streamer's error method.
func (ss *speedStreamer) Err() error {
	return ss.s.Err()
}
