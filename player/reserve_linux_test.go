//go:build linux && cgo

package player

import "testing"

func TestParseALSACardToken(t *testing.T) {
	tests := []struct {
		name      string
		device    string
		wantToken string
		wantOK    bool
	}{
		{name: "numeric hw", device: "hw:2,0", wantToken: "2", wantOK: true},
		{name: "named hw", device: "hw:K6,0", wantToken: "K6", wantOK: true},
		{name: "CARD= form", device: "hw:CARD=PCH,DEV=0", wantToken: "PCH", wantOK: true},
		{name: "plughw CARD= form", device: "plughw:CARD=Generic,DEV=0", wantToken: "Generic", wantOK: true},
		{name: "no comma", device: "hw:2", wantToken: "2", wantOK: true},
		{name: "empty after prefix", device: "hw:", wantToken: "", wantOK: false},
		{name: "default is not hw", device: "default", wantToken: "", wantOK: false},
		{name: "pipewire is not hw", device: "pipewire", wantToken: "", wantOK: false},
		{name: "pulse is not hw", device: "pulse", wantToken: "", wantOK: false},
		{name: "empty string", device: "", wantToken: "", wantOK: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotToken, gotOK := parseALSACardToken(tt.device)
			if gotToken != tt.wantToken || gotOK != tt.wantOK {
				t.Errorf("parseALSACardToken(%q) = (%q, %v), want (%q, %v)",
					tt.device, gotToken, gotOK, tt.wantToken, tt.wantOK)
			}
		})
	}
}

func TestReserveBusName(t *testing.T) {
	tests := []struct {
		cardIdx int
		want    string
	}{
		{cardIdx: 0, want: "org.freedesktop.ReserveDevice1.Audio0"},
		{cardIdx: 2, want: "org.freedesktop.ReserveDevice1.Audio2"},
		{cardIdx: 10, want: "org.freedesktop.ReserveDevice1.Audio10"},
	}

	for _, tt := range tests {
		if got := reserveBusName(tt.cardIdx); got != tt.want {
			t.Errorf("reserveBusName(%d) = %q, want %q", tt.cardIdx, got, tt.want)
		}
	}
}

func TestReserveObjectPath(t *testing.T) {
	tests := []struct {
		cardIdx int
		want    string
	}{
		{cardIdx: 0, want: "/org/freedesktop/ReserveDevice1/Audio0"},
		{cardIdx: 2, want: "/org/freedesktop/ReserveDevice1/Audio2"},
		{cardIdx: 10, want: "/org/freedesktop/ReserveDevice1/Audio10"},
	}

	for _, tt := range tests {
		if got := reserveObjectPath(tt.cardIdx); string(got) != tt.want {
			t.Errorf("reserveObjectPath(%d) = %q, want %q", tt.cardIdx, got, tt.want)
		}
	}
}

func TestDeviceReservationReleaseNilSafe(t *testing.T) {
	var r *deviceReservation
	r.release() // must not panic
}
