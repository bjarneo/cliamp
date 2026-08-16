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
// and returns the number of bytes written. dst must hold at least
// len(frames)*enc.bytesPerFrame() bytes.
func encodePCM(frames [][2]float64, dst []byte, enc pcmEncoding) int {
	bps := enc.bytesPerSample()
	fs := enc.bytesPerFrame()
	switch enc {
	case pcmS32LE:
		for i, f := range frames {
			binary.LittleEndian.PutUint32(dst[i*fs:], uint32(scaleInt32(f[0])))
			binary.LittleEndian.PutUint32(dst[i*fs+bps:], uint32(scaleInt32(f[1])))
		}
	case pcmFloat32LE:
		for i, f := range frames {
			binary.LittleEndian.PutUint32(dst[i*fs:], math.Float32bits(float32(clampUnit(f[0]))))
			binary.LittleEndian.PutUint32(dst[i*fs+bps:], math.Float32bits(float32(clampUnit(f[1]))))
		}
	default:
		for i, f := range frames {
			binary.LittleEndian.PutUint16(dst[i*fs:], uint16(scaleInt16(f[0])))
			binary.LittleEndian.PutUint16(dst[i*fs+bps:], uint16(scaleInt16(f[1])))
		}
	}
	return len(frames) * fs
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
