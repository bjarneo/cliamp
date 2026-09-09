package ui

import "testing"

func TestParseFrequency(t *testing.T) {
	tests := []struct {
		input    string
		wantFreq float64
		wantBand string
	}{
		{"88.3 FM", 88.3, "FM"},
		{"91.7FM", 91.7, "FM"},
		{"90.3", 90.3, "FM"},
		{"1200 AM", 1200, "AM"},
		{"880AM", 880, "AM"},
		{"660", 660, "AM"},
		{"", 0, ""},
		{"not a number", 0, ""},
	}
	for _, tt := range tests {
		freq, band := ParseFrequency(tt.input)
		if freq != tt.wantFreq || band != tt.wantBand {
			t.Errorf("ParseFrequency(%q) = (%v, %q), want (%v, %q)",
				tt.input, freq, band, tt.wantFreq, tt.wantBand)
		}
	}
}

func TestRenderRadioDialNonEmpty(t *testing.T) {
	out := RenderRadioDial("91.7 FM", 60)
	if out == "" {
		t.Fatal("expected non-empty dial output for valid frequency")
	}
}

func TestRenderRadioDialEmpty(t *testing.T) {
	out := RenderRadioDial("", 60)
	if out != "" {
		t.Fatalf("expected empty output for empty frequency, got %q", out)
	}
	out = RenderRadioDial("bad", 60)
	if out != "" {
		t.Fatalf("expected empty output for unparseable frequency, got %q", out)
	}
}

func TestRenderRadioDialAM(t *testing.T) {
	out := RenderRadioDial("1200 AM", 60)
	if out == "" {
		t.Fatal("expected non-empty dial output for AM frequency")
	}
}
