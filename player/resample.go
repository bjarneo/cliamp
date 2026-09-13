package player

import "github.com/gopxl/beep/v2"

// resampleHeadroomGain is the linear gain applied before sample-rate
// conversion to absorb intersample overs: beep.Resample's windowed-sinc
// interpolation can overshoot a source that peaks at or near 0dBFS (typical
// for loudness-normalized Icecast/Shoutcast radio streams), producing hard
// clipping at the final int16 output stage even though no single input
// sample exceeded full scale. -2dBFS of headroom leaves enough margin for
// that overshoot (measured up to ~20% on synthetic worst-case broadcast-like
// content, ~10% on a real hot-mastered Icecast stream) to stay within
// [-1, 1] without an audible level change.
const resampleHeadroomGain = 0.7943282347242815 // 10^(-2/20)

// headroomStreamer scales every sample by a fixed linear gain.
type headroomStreamer struct {
	s    beep.Streamer
	gain float64
}

func (h *headroomStreamer) Stream(samples [][2]float64) (n int, ok bool) {
	n, ok = h.s.Stream(samples)
	for i := range n {
		samples[i][0] *= h.gain
		samples[i][1] *= h.gain
	}
	return n, ok
}

func (h *headroomStreamer) Err() error { return h.s.Err() }

// resampleWithHeadroom resamples s from 'from' to 'to', applying a small
// gain reduction beforehand to prevent the resampler's interpolation
// overshoot from hard-clipping at the final output stage. When no
// resampling is needed, s is returned unchanged.
func resampleWithHeadroom(quality int, from, to beep.SampleRate, s beep.Streamer) beep.Streamer {
	if from == to {
		return s
	}
	return beep.Resample(quality, from, to, &headroomStreamer{s: s, gain: resampleHeadroomGain})
}
