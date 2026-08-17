package model

import "testing"

func TestFormatKHz(t *testing.T) {
	tests := []struct {
		hz   int
		want string
	}{
		{hz: 44100, want: "44.1kHz"},
		{hz: 48000, want: "48kHz"},
		{hz: 96000, want: "96kHz"},
		{hz: 192000, want: "192kHz"},
		{hz: 22050, want: "22.1kHz"},
	}

	for _, tt := range tests {
		if got := formatKHz(tt.hz); got != tt.want {
			t.Errorf("formatKHz(%d) = %q, want %q", tt.hz, got, tt.want)
		}
	}
}
