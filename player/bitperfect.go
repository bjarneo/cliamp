package player

import "fmt"

// BitPerfectStatus describes the transparency of the current output path.
//
// Active means every stage between the decoder and the device is a pass-through:
// no resampling, no bit-depth reduction, and no DSP. That requires the volume at
// 0 dB, a flat EQ, no mono downmix and normal playback speed — anything else
// rewrites the samples, however slightly.
type BitPerfectStatus struct {
	Enabled    bool   // bit-perfect mode is on and the backend supports it
	Active     bool   // the signal path is currently bit-exact
	SourceRate int    // the track's native sample rate in Hz (0 when unknown)
	SourceBits int    // the source's own, independently confirmed bit depth, e.g. 16 or 24 (0 when unverified)
	DeviceRate int    // the rate the output device is running at, in Hz
	Encoding   string // PCM format handed to the device, e.g. "S32_LE"
	Blocker    string // what prevents Active; empty when Active
}

// bitPerfectInputs is the snapshot BitPerfect() evaluates. Keeping the decision
// in one pure method makes the rules testable and keeps the indicator from
// disagreeing with what the pipeline actually does.
type bitPerfectInputs struct {
	enabled     bool
	playing     bool
	sourceRate  int
	deviceRate  int
	rateExact   bool
	encoding    pcmEncoding
	sourceBytes int // beep.Format.Precision: bytes per sample as delivered by the decoder (0 = unknown) — see verifiedSourceBits below for why this isn't the same claim as "the source's own bit depth"
	// verifiedSourceBits is the source's own bit depth, independently
	// confirmed rather than assumed — the display-only counterpart to
	// sourceBytes above, and for the same reason verifiedSourceRate exists
	// alongside format.SampleRate on trackPipeline: an FFmpeg-decoded
	// source (ALAC, M4A, AAC, Opus, WMA, WebM) always decodes to 32-bit
	// float in bit-perfect mode regardless of what the source actually is,
	// so sourceBytes there reflects the decode target, not the source —
	// reporting it as a bit-depth claim would show 32-bit for a 24-bit
	// ALAC file. Zero means unverified. See trackPipeline.verifiedSourceBits.
	verifiedSourceBits int
	volumeDB           float64
	eq                 [10]float64
	mono               bool
	speed              float64
	note               string // backend-reported problem, e.g. a refused rate
}

// eval applies the bit-perfect rules and returns the resulting status. Blockers
// are reported most-fundamental first, so the message names the thing the user
// has to fix before anything else matters.
func (in bitPerfectInputs) eval() BitPerfectStatus {
	st := BitPerfectStatus{
		Enabled:    in.enabled,
		SourceRate: in.sourceRate,
		DeviceRate: in.deviceRate,
		Encoding:   in.encoding.String(),
	}
	if in.sourceRate > 0 {
		// Only ever paired with a known source rate: the two probes behind
		// sourceRate and verifiedSourceBits (see trackPipeline's buffered-URL
		// construction site) run independently and can disagree on success,
		// and "Xbit/Ykhz" reads as one verified source spec — showing bits
		// alone next to the device's rate would misrepresent the device's
		// rate as the source's.
		st.SourceBits = in.verifiedSourceBits
	}

	switch {
	case in.note != "":
		st.Blocker = in.note
	case !in.enabled:
		st.Blocker = "bit-perfect mode is off"
	case !in.playing:
		st.Blocker = "nothing playing"
	case in.sourceRate == 0:
		st.Blocker = "source sample rate unknown"
	case in.sourceRate != in.deviceRate:
		st.Blocker = fmt.Sprintf("resampling %d Hz to %d Hz", in.sourceRate, in.deviceRate)
	case !in.rateExact:
		st.Blocker = "device is not locked to the source rate"
	case in.sourceBytes > in.encoding.bytesPerSample():
		st.Blocker = fmt.Sprintf("%d-bit source reduced to %s", in.sourceBytes*8, in.encoding)
	case in.volumeDB != 0:
		st.Blocker = fmt.Sprintf("volume at %+.1f dB", in.volumeDB)
	case !eqFlat(in.eq):
		st.Blocker = "EQ is not flat"
	case in.mono:
		st.Blocker = "mono downmix is on"
	case in.speed != 1:
		st.Blocker = fmt.Sprintf("playback speed at %.2fx", in.speed)
	default:
		st.Active = true
	}
	return st
}

// eqFlat reports whether every band is within the bypass window.
func eqFlat(bands [10]float64) bool {
	for _, dB := range bands {
		if !eqBypassed(dB) {
			return false
		}
	}
	return true
}
