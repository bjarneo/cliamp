package ui

import "time"

// Tick intervals: fast for visualizer animation, slow for time/seek display.
const (
	TickFast = 50 * time.Millisecond  // 20 FPS — visualizer active
	TickSlow = 200 * time.Millisecond // 5 FPS — visualizer off or overlay
)
