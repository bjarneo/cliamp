package ui

import "time"

// Tick intervals: fast for visualizer animation, slow for time/seek display.
const (
	TickWave = 32 * time.Millisecond  // ~30 FPS — waveform modes (no FFT)
	TickFast = 50 * time.Millisecond  // 20 FPS — spectrum modes (FFT)
	TickSlow = 200 * time.Millisecond // 5 FPS — visualizer off or overlay
)
