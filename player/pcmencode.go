package player

import (
	"encoding/binary"
	"math"
)

// pcmEncoding is the sample format a sink hands to the audio device.
//
// The scale factors below are deliberately powers of two. Every beep decoder
// produces float64 samples by dividing the integer sample by 2^(bits-1), so
// multiplying back by 2^(bits-1) of the target format reproduces the original
// integer exactly (left-shifted into the wider container). Scaling by
// 2^(bits-1)-1 instead — as most players do — introduces a rounding error of up
// to one LSB on every sample and makes true bit-perfect output impossible.
type pcmEncoding int

const (
	// pcmS32LE is 32-bit signed integer PCM. Preferred for bit-perfect output:
	// it carries 16- and 24-bit sources without any loss.
	pcmS32LE pcmEncoding = iota
	// pcmFloat32LE is 32-bit float PCM. Its 24-bit mantissa carries 16- and
	// 24-bit integer sources exactly.
	pcmFloat32LE
	// pcmS16LE is 16-bit signed integer PCM. Lossless for 16-bit sources only.
	pcmS16LE
)

// bytesPerSample returns the encoded size of a single channel sample. Reuses
// the pipe frame-size constants from ffmpeg.go (halved to a per-channel size)
// so the two PCM systems can't drift apart.
func (e pcmEncoding) bytesPerSample() int {
	if e == pcmS16LE {
		return pcmFrameSize16 / 2
	}
	return pcmFrameSize32 / 2
}

// bytesPerFrame returns the encoded size of one stereo frame.
func (e pcmEncoding) bytesPerFrame() int { return 2 * e.bytesPerSample() }

// String returns the ALSA-style format name, used in the UI and logs.
func (e pcmEncoding) String() string {
	switch e {
	case pcmS32LE:
		return "S32_LE"
	case pcmFloat32LE:
		return "FLOAT_LE"
	default:
		return "S16_LE"
	}
}

// encodePCM converts stereo float64 frames into interleaved little-endian PCM
// for a plain 2-channel device and returns the number of bytes written. dst
// must hold at least len(frames)*enc.bytesPerFrame() bytes. See
// encodePCMChannels for a device whose channel count isn't 2.
func encodePCM(frames [][2]float64, dst []byte, enc pcmEncoding) int {
	bps := enc.bytesPerSample()
	fs := enc.bytesPerFrame()
	for i, f := range frames {
		putSample(dst[i*fs:], f[0], enc)
		putSample(dst[i*fs+bps:], f[1], enc)
	}
	return len(frames) * fs
}

// encodePCMChannels is encodePCM generalized to a device whose negotiated
// channel count isn't 2 — a multichannel interface with no native stereo
// mode (see bitperfect_channels in the config) still gets exactly the same
// L/R samples, placed at channel indices left/right within each channels-wide
// frame; every other channel in the frame is silence. channels==2, left==0,
// right==1 produces byte-identical output to encodePCM, but costs an extra
// zeroing pass, so callers on that common path should prefer encodePCM.
// dst must hold at least len(frames)*channels*enc.bytesPerSample() bytes.
func encodePCMChannels(frames [][2]float64, dst []byte, enc pcmEncoding, channels, left, right int) int {
	bps := enc.bytesPerSample()
	fs := channels * bps
	clear(dst[:len(frames)*fs])
	for i, f := range frames {
		base := i * fs
		putSample(dst[base+left*bps:], f[0], enc)
		putSample(dst[base+right*bps:], f[1], enc)
	}
	return len(frames) * fs
}

// putSample writes one sample in enc's format, shared by encodePCM and
// encodePCMChannels.
func putSample(dst []byte, v float64, enc pcmEncoding) {
	switch enc {
	case pcmS32LE:
		binary.LittleEndian.PutUint32(dst, uint32(scaleInt32(v)))
	case pcmFloat32LE:
		binary.LittleEndian.PutUint32(dst, math.Float32bits(float32(clampUnit(v))))
	default:
		binary.LittleEndian.PutUint16(dst, uint16(scaleInt16(v)))
	}
}

// clampUnit constrains a sample to the [-1, 1] range the device expects.
func clampUnit(v float64) float64 { return max(min(v, 1), -1) }

// scaleInt32 converts a normalized sample to 32-bit signed PCM. Negative
// full scale (-1.0) maps exactly to -2^31; positive samples saturate one step
// below +2^31 because that value is not representable.
func scaleInt32(v float64) int32 {
	scaled := math.Round(clampUnit(v) * (1 << 31))
	if scaled > math.MaxInt32 {
		return math.MaxInt32
	}
	return int32(scaled)
}

// scaleInt16 converts a normalized sample to 16-bit signed PCM.
func scaleInt16(v float64) int16 {
	scaled := math.Round(clampUnit(v) * (1 << 15))
	if scaled > math.MaxInt16 {
		return math.MaxInt16
	}
	return int16(scaled)
}
