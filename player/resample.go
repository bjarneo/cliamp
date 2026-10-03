package player

import "github.com/gopxl/beep/v2"

// resampleHeadroomGain is the linear gain applied before sample-rate
// conversion to absorb intersample overs: beep.Resample's windowed-sinc
// interpolation can overshoot a signal that peaks at or near full scale
// (typical for loudness-normalized Icecast/Shoutcast radio streams), which
// the final int16 output stage turns into hard clipping even though no
// input sample exceeded full scale. A full-scale square wave rings up to
// ~1.20 on a 22.05kHz to 44.1kHz conversion; this gain brings it to ~0.95.
// The level change is inaudible, and hot broadcast material stays below
// full scale after conversion.
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
// gain reduction beforehand so interpolation overshoot does not clip at the
// final output stage. When no resampling is needed, s is returned unchanged.
func resampleWithHeadroom(quality int, from, to beep.SampleRate, s beep.Streamer) beep.Streamer {
	if from == to {
		return s
	}
	return beep.Resample(quality, from, to, &headroomStreamer{s: s, gain: resampleHeadroomGain})
}
