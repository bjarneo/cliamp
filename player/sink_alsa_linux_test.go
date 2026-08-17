//go:build linux && cgo

package player

import "testing"

func TestIsVirtualALSADevice(t *testing.T) {
	tests := []struct {
		device string
		want   bool
	}{
		{"default", true},
		{"pipewire", true},
		{"pulse", true},
		{"sysdefault:CARD=PCH", true},
		{"hw:0,0", false},
		{"hw:CARD=PCH,DEV=0", false},
		{"plughw:0,0", false},
	}
	for _, tt := range tests {
		if got := isVirtualALSADevice(tt.device); got != tt.want {
			t.Errorf("isVirtualALSADevice(%q) = %v, want %v", tt.device, got, tt.want)
		}
	}
}

func TestIsRawHardwareDevice(t *testing.T) {
	tests := []struct {
		device string
		want   bool
	}{
		{"hw:0,0", true},
		{"hw:CARD=PCH,DEV=0", true},
		{"plughw:0,0", false},
		{"plughw:CARD=PCH,DEV=0", false},
		{"default", false},
		{"pipewire", false},
		{"pulse", false},
		{"sysdefault:CARD=PCH", false},
	}
	for _, tt := range tests {
		if got := isRawHardwareDevice(tt.device); got != tt.want {
			t.Errorf("isRawHardwareDevice(%q) = %v, want %v", tt.device, got, tt.want)
		}
	}
}
