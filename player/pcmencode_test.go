package player

import (
	"math"
	"testing"
)

func TestEncodingSizes(t *testing.T) {
	tests := []struct {
		enc               pcmEncoding
		wantBytesPerSamp  int
		wantBytesPerFrame int
		wantString        string
	}{
		{pcmS32LE, 4, 8, "S32_LE"},
		{pcmFloat32LE, 4, 8, "FLOAT_LE"},
		{pcmS16LE, 2, 4, "S16_LE"},
	}
	for _, tt := range tests {
		if got := tt.enc.bytesPerSample(); got != tt.wantBytesPerSamp {
			t.Errorf("%v.bytesPerSample() = %d, want %d", tt.enc, got, tt.wantBytesPerSamp)
		}
		if got := tt.enc.bytesPerFrame(); got != tt.wantBytesPerFrame {
			t.Errorf("%v.bytesPerFrame() = %d, want %d", tt.enc, got, tt.wantBytesPerFrame)
		}
		if got := tt.enc.String(); got != tt.wantString {
			t.Errorf("%v.String() = %q, want %q", tt.enc, got, tt.wantString)
		}
	}
}

// TestS16RoundTripIsLossless verifies that every possible 16-bit sample,
// normalized to float64 the way beep decoders do, survives an S16_LE
// encode/decode round trip exactly. This is the property bit-perfect mode
// depends on for 16-bit sources.
func TestS16RoundTripIsLossless(t *testing.T) {
	buf := make([]byte, pcmS16LE.bytesPerFrame())
	for v := -32768; v <= 32767; v++ {
		want := int16(v)
		norm := float64(want) / 32768
		encodePCM([][2]float64{{norm, norm}}, buf, pcmS16LE)
		got := int16(uint16(buf[0]) | uint16(buf[1])<<8)
		if got != want {
			t.Fatalf("S16 round trip: in=%d norm=%v out=%d", want, norm, got)
		}
	}
}

// TestS32RoundTripIsLosslessFor16And24Bit checks that S32_LE, the preferred
// bit-perfect format, reproduces both 16-bit and 24-bit normalized samples
// exactly — the two bit depths cliamp's decoders actually produce.
func TestS32RoundTripIsLosslessFor16And24Bit(t *testing.T) {
	buf := make([]byte, pcmS32LE.bytesPerFrame())

	// 16-bit: every sample value spread across the wider container.
	for v := -32768; v <= 32767; v += 137 { // stride keeps the test fast
		want := int32(v) << 16
		norm := float64(int16(v)) / 32768
		encodePCM([][2]float64{{norm, 0}}, buf, pcmS32LE)
		got := int32(uint32(buf[0]) | uint32(buf[1])<<8 | uint32(buf[2])<<16 | uint32(buf[3])<<24)
		if got != want {
			t.Fatalf("S32 round trip (16-bit source) v=%d: got %d, want %d", v, got, want)
		}
	}

	// 24-bit: full range, sampled at a stride.
	const s24Max = 1 << 23
	for v := -s24Max; v < s24Max; v += 99999 {
		want := int32(v) << 8
		norm := float64(v) / s24Max
		encodePCM([][2]float64{{norm, 0}}, buf, pcmS32LE)
		got := int32(uint32(buf[0]) | uint32(buf[1])<<8 | uint32(buf[2])<<16 | uint32(buf[3])<<24)
		if got != want {
			t.Fatalf("S32 round trip (24-bit source) v=%d: got %d, want %d", v, got, want)
		}
	}
}

func TestFloat32EncodePreservesValue(t *testing.T) {
	buf := make([]byte, pcmFloat32LE.bytesPerFrame())
	samples := [][2]float64{{0.5, -0.25}}
	encodePCM(samples, buf, pcmFloat32LE)

	left := math.Float32frombits(uint32(buf[0]) | uint32(buf[1])<<8 | uint32(buf[2])<<16 | uint32(buf[3])<<24)
	right := math.Float32frombits(uint32(buf[4]) | uint32(buf[5])<<8 | uint32(buf[6])<<16 | uint32(buf[7])<<24)
	if left != 0.5 {
		t.Errorf("left = %v, want 0.5", left)
	}
	if right != -0.25 {
		t.Errorf("right = %v, want -0.25", right)
	}
}

func TestEncodePCMReturnsBytesWritten(t *testing.T) {
	frames := make([][2]float64, 10)
	buf := make([]byte, 10*pcmS32LE.bytesPerFrame())
	if n := encodePCM(frames, buf, pcmS32LE); n != 80 {
		t.Errorf("encodePCM(S32) = %d bytes, want 80", n)
	}
	buf16 := make([]byte, 10*pcmS16LE.bytesPerFrame())
	if n := encodePCM(frames, buf16, pcmS16LE); n != 40 {
		t.Errorf("encodePCM(S16) = %d bytes, want 40", n)
	}
}

// TestEncodePCMChannelsMatchesPlainStereo verifies that channels==2,
// left==0, right==1 — the common, unconfigured case — produces byte-identical
// output to encodePCM, since writeLoop picks between the two based on that
// exact condition and both must agree.
func TestEncodePCMChannelsMatchesPlainStereo(t *testing.T) {
	frames := [][2]float64{{0.5, -0.25}, {-1, 1}, {0, 0.1}}
	for _, enc := range []pcmEncoding{pcmS32LE, pcmFloat32LE, pcmS16LE} {
		want := make([]byte, len(frames)*enc.bytesPerFrame())
		encodePCM(frames, want, enc)

		got := make([]byte, len(frames)*enc.bytesPerFrame())
		n := encodePCMChannels(frames, got, enc, 2, 0, 1)

		if n != len(want) {
			t.Errorf("%v: encodePCMChannels returned %d bytes, want %d", enc, n, len(want))
		}
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("%v: byte %d = %#x, want %#x (encodePCMChannels diverged from encodePCM)", enc, i, got[i], want[i])
			}
		}
	}
}

// TestEncodePCMChannelsPlacesStereoAndSilencesTheRest verifies a 6-channel
// frame (the Komplete Audio 6 shape that motivated this) with left/right at
// non-zero indices: the L/R samples land at the configured indices and every
// other channel in the frame is silence.
func TestEncodePCMChannelsPlacesStereoAndSilencesTheRest(t *testing.T) {
	frames := [][2]float64{{1, -1}}
	const channels, left, right = 6, 2, 3
	buf := make([]byte, len(frames)*channels*pcmS32LE.bytesPerSample())

	n := encodePCMChannels(frames, buf, pcmS32LE, channels, left, right)
	if want := len(buf); n != want {
		t.Fatalf("encodePCMChannels returned %d bytes, want %d", n, want)
	}

	bps := pcmS32LE.bytesPerSample()
	for ch := range channels {
		slot := buf[ch*bps : (ch+1)*bps]
		var wantSample float64
		switch ch {
		case left:
			wantSample = 1
		case right:
			wantSample = -1
		default:
			wantSample = 0
		}
		wantBuf := make([]byte, bps)
		putSample(wantBuf, wantSample, pcmS32LE)
		for i := range wantBuf {
			if slot[i] != wantBuf[i] {
				t.Errorf("channel %d byte %d = %#x, want %#x", ch, i, slot[i], wantBuf[i])
			}
		}
	}
}

func TestScaleClampsOutOfRangeSamples(t *testing.T) {
	if got := scaleInt16(2.0); got != math.MaxInt16 {
		t.Errorf("scaleInt16(2.0) = %d, want %d", got, int16(math.MaxInt16))
	}
	if got := scaleInt16(-2.0); got != math.MinInt16 {
		t.Errorf("scaleInt16(-2.0) = %d, want %d", got, int16(math.MinInt16))
	}
	if got := scaleInt32(2.0); got != math.MaxInt32 {
		t.Errorf("scaleInt32(2.0) = %d, want %d", got, int32(math.MaxInt32))
	}
	if got := scaleInt32(-1.0); got != math.MinInt32 {
		t.Errorf("scaleInt32(-1.0) = %d, want %d", got, int32(math.MinInt32))
	}
}
