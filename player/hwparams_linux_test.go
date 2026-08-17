//go:build linux && cgo

package player

import (
	"os"
	"path/filepath"
	"testing"
)

func TestAlsaDeviceIndex(t *testing.T) {
	tests := []struct {
		device string
		want   int
	}{
		{"hw:2,0", 0},
		{"hw:2,1", 1},
		{"hw:K6,0", 0},
		{"hw:CARD=PCH,DEV=1", 1},
		{"plughw:CARD=Generic,DEV=0", 0},
		{"hw:2", 0},
		{"hw:", 0},
		{"default", 0},
	}
	for _, tt := range tests {
		if got := alsaDeviceIndex(tt.device); got != tt.want {
			t.Errorf("alsaDeviceIndex(%q) = %d, want %d", tt.device, got, tt.want)
		}
	}
}

func TestRealALSARate(t *testing.T) {
	dir := t.TempDir()
	write := func(name, content string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("open.txt", "access: MMAP_INTERLEAVED\nformat: S32_LE\nchannels: 6\nrate: 96000 (96000/1)\nperiod_size: 2400\n")
	write("closed.txt", "closed\n")
	write("garbage.txt", "rate: not-a-number\n")
	write("empty.txt", "")

	tests := []struct {
		name     string
		file     string
		wantRate int
		wantOK   bool
	}{
		{"open substream", "open.txt", 96000, true},
		{"closed substream", "closed.txt", 0, false},
		{"unparseable rate", "garbage.txt", 0, false},
		{"empty file", "empty.txt", 0, false},
		{"missing file", "does-not-exist.txt", 0, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotRate, gotOK := realALSARateFile(filepath.Join(dir, tt.file))
			if gotRate != tt.wantRate || gotOK != tt.wantOK {
				t.Errorf("realALSARateFile(%q) = (%d, %v), want (%d, %v)",
					tt.file, gotRate, gotOK, tt.wantRate, tt.wantOK)
			}
		})
	}
}
