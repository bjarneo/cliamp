package player

import "testing"

// baseInputs returns a snapshot that satisfies every bit-perfect rule, so each
// test case can flip exactly one field and see exactly one blocker.
func baseInputs() bitPerfectInputs {
	return bitPerfectInputs{
		enabled:     true,
		playing:     true,
		sourceRate:  44100,
		deviceRate:  44100,
		rateExact:   true,
		encoding:    pcmS32LE,
		sourceBytes: 2,
		speed:       1,
	}
}

func TestBitPerfectEvalActiveWhenEverythingLinesUp(t *testing.T) {
	st := baseInputs().eval()
	if !st.Active {
		t.Fatalf("Active = false, want true; blocker = %q", st.Blocker)
	}
	if st.Blocker != "" {
		t.Errorf("Blocker = %q, want empty when Active", st.Blocker)
	}
}

func TestBitPerfectEvalBlockers(t *testing.T) {
	tests := []struct {
		name   string
		modify func(*bitPerfectInputs)
	}{
		{"disabled", func(in *bitPerfectInputs) { in.enabled = false }},
		{"backend note", func(in *bitPerfectInputs) { in.note = "device refused 96000 Hz" }},
		{"not playing", func(in *bitPerfectInputs) { in.playing = false }},
		{"unknown source rate", func(in *bitPerfectInputs) { in.sourceRate = 0 }},
		{"rate mismatch", func(in *bitPerfectInputs) { in.deviceRate = 48000 }},
		{"device not exact", func(in *bitPerfectInputs) { in.rateExact = false }},
		{"depth reduced", func(in *bitPerfectInputs) { in.sourceBytes = 8 }},
		{"volume not 0dB", func(in *bitPerfectInputs) { in.volumeDB = -3 }},
		{"eq not flat", func(in *bitPerfectInputs) { in.eq[3] = 2 }},
		{"mono on", func(in *bitPerfectInputs) { in.mono = true }},
		{"speed changed", func(in *bitPerfectInputs) { in.speed = 1.25 }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			in := baseInputs()
			tt.modify(&in)
			st := in.eval()
			if st.Active {
				t.Fatalf("Active = true, want false for modification %q", tt.name)
			}
			if st.Blocker == "" {
				t.Errorf("Blocker is empty, want a reason for %q", tt.name)
			}
		})
	}
}

func TestBitPerfectEvalPrefersNoteOverOtherBlockers(t *testing.T) {
	in := baseInputs()
	in.note = "device refused 96000 Hz: bad rate"
	in.volumeDB = -6 // would also block, but the device-level problem wins
	st := in.eval()
	if st.Blocker != in.note {
		t.Errorf("Blocker = %q, want backend note %q", st.Blocker, in.note)
	}
}

func TestBitPerfectEvalNoteSurvivesDisabledFallback(t *testing.T) {
	// When the ALSA backend fails to open, Player.New falls back to the beep
	// sink with enabled=false but still records why. That reason — not the
	// generic "mode is off" — should be what the user sees.
	in := baseInputs()
	in.enabled = false
	in.note = "bit-perfect output unavailable: alsa \"default\" at 44100 Hz: open: no such device"
	st := in.eval()
	if st.Blocker != in.note {
		t.Errorf("Blocker = %q, want backend note %q", st.Blocker, in.note)
	}
}

func TestEqFlatWithinBypassEpsilon(t *testing.T) {
	bands := [10]float64{}
	bands[0] = 0.05 // inside the biquad's own bypass window
	if !eqFlat(bands) {
		t.Error("eqFlat() = false for a band within the bypass epsilon, want true")
	}
	bands[0] = 0.5
	if eqFlat(bands) {
		t.Error("eqFlat() = true for a band outside the bypass epsilon, want false")
	}
}

func TestBitPerfectStatusReportsRatesAndEncoding(t *testing.T) {
	in := baseInputs()
	in.sourceRate = 96000
	in.deviceRate = 96000
	in.encoding = pcmFloat32LE
	st := in.eval()
	if st.SourceRate != 96000 || st.DeviceRate != 96000 {
		t.Errorf("rates = (%d, %d), want (96000, 96000)", st.SourceRate, st.DeviceRate)
	}
	if st.Encoding != "FLOAT_LE" {
		t.Errorf("Encoding = %q, want FLOAT_LE", st.Encoding)
	}
}

func TestBitPerfectStatusReportsSourceBits(t *testing.T) {
	tests := []struct {
		name               string
		verifiedSourceBits int
		want               int
	}{
		{"16-bit source", 16, 16},
		{"24-bit source", 24, 24},
		{"unverified", 0, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			in := baseInputs()
			in.verifiedSourceBits = tt.verifiedSourceBits
			if got := in.eval().SourceBits; got != tt.want {
				t.Errorf("SourceBits = %d, want %d", got, tt.want)
			}
		})
	}
}

// TestBitPerfectStatusDoesNotLeakDecodeWidthAsSourceBits guards the exact
// regression a real 24-bit ALAC/M4A file hit: bit-perfect mode always
// decodes FFmpeg sources through 32-bit float (sourceBytes=4) regardless of
// the source's real depth, and SourceBits must not echo that back as if it
// were a verified claim about the source.
func TestBitPerfectStatusDoesNotLeakDecodeWidthAsSourceBits(t *testing.T) {
	in := baseInputs()
	in.sourceBytes = 4 // the 32-bit float FFmpeg always decodes to in bit-perfect mode
	in.verifiedSourceBits = 0
	if got := in.eval().SourceBits; got != 0 {
		t.Errorf("SourceBits = %d, want 0 (unverified) even though sourceBytes=4", got)
	}
}
