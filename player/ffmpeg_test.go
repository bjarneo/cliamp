package player

import (
	"bufio"
	"bytes"
	"errors"
	"io"
	"testing"
	"time"
)

// TestFFmpegPipeLiveEOF verifies that an unexpected EOF on an infinite radio
// stream is surfaced as an error (so auto-reconnect fires), while a finite
// stream treats EOF as a clean end-of-track.
func TestFFmpegPipeLiveEOF(t *testing.T) {
	tests := []struct {
		name    string
		live    bool
		wantErr bool
	}{
		{name: "live stream EOF is an error", live: true, wantErr: true},
		{name: "finite stream EOF is clean", live: false, wantErr: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := &ffmpegPipe{
				reader: bufio.NewReader(bytes.NewReader(nil)), // immediate EOF
				live:   tt.live,
			}
			samples := make([][2]float64, 8)
			n, ok := f.Stream(samples)
			if n != 0 || ok {
				t.Fatalf("Stream at EOF: got n=%d ok=%v, want 0, false", n, ok)
			}
			if gotErr := f.Err() != nil; gotErr != tt.wantErr {
				t.Fatalf("Err()=%v, wantErr=%v", f.Err(), tt.wantErr)
			}
			if tt.wantErr && !errors.Is(f.Err(), io.ErrUnexpectedEOF) {
				t.Fatalf("Err()=%v, want io.ErrUnexpectedEOF", f.Err())
			}
		})
	}
}

// TestProbeNativeRateUnreadable verifies probeNativeRate fails closed (0, not
// a panic or hang) for a path ffprobe can't read — the same outcome the
// buffered-URL pipeline relies on to fall back to the device's current rate.
func TestProbeNativeRateUnreadable(t *testing.T) {
	if got := probeNativeRate("/nonexistent/path/does-not-exist.flac"); got != 0 {
		t.Errorf("probeNativeRate(nonexistent) = %d, want 0", got)
	}
}

// TestProbeNativeRateAsyncDeliversOnce verifies the async wrapper used by the
// buffered-URL pipeline sends exactly one value and doesn't hang, so the
// pipeline's select-with-timeout around it is safe to rely on.
func TestProbeNativeRateAsyncDeliversOnce(t *testing.T) {
	ch := probeNativeRateAsync("/nonexistent/path/does-not-exist.flac")
	select {
	case got := <-ch:
		if got != 0 {
			t.Errorf("probeNativeRateAsync result = %d, want 0", got)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("probeNativeRateAsync did not deliver a result in time")
	}
}
